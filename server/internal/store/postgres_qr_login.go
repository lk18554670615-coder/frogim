package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const qrLoginTicketColumns = `id,qr_token_hash,poll_token_hash,COALESCE(user_id,''),client_platform,client_name,created_at,expires_at,confirmed_at,consumed_at`

func scanQRLoginTicket(row pgx.Row) (QRLoginTicket, error) {
	var ticket QRLoginTicket
	err := row.Scan(
		&ticket.ID,
		&ticket.QRTokenHash,
		&ticket.PollTokenHash,
		&ticket.UserID,
		&ticket.ClientPlatform,
		&ticket.ClientName,
		&ticket.CreatedAt,
		&ticket.ExpiresAt,
		&ticket.ConfirmedAt,
		&ticket.ConsumedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return QRLoginTicket{}, ErrNotFound
	}
	return ticket, err
}

func (p *Postgres) CreateQRLoginTicket(ctx context.Context, ticket QRLoginTicket) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `DELETE FROM im_qr_login_tickets WHERE expires_at<$1`, ticket.CreatedAt.Add(-24*time.Hour)); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_qr_login_tickets(id,qr_token_hash,poll_token_hash,client_platform,client_name,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, ticket.ID, ticket.QRTokenHash, ticket.PollTokenHash, ticket.ClientPlatform, ticket.ClientName, ticket.CreatedAt, ticket.ExpiresAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) GetQRLoginTicketByToken(ctx context.Context, hash []byte) (QRLoginTicket, error) {
	return scanQRLoginTicket(p.pool.QueryRow(ctx, `SELECT `+qrLoginTicketColumns+` FROM im_qr_login_tickets WHERE qr_token_hash=$1`, hash))
}

func (p *Postgres) ConfirmQRLoginTicket(ctx context.Context, hash []byte, uid string, at time.Time) (QRLoginTicket, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return QRLoginTicket{}, err
	}
	defer tx.Rollback(ctx)
	ticket, err := scanQRLoginTicket(tx.QueryRow(ctx, `SELECT `+qrLoginTicketColumns+` FROM im_qr_login_tickets WHERE qr_token_hash=$1 FOR UPDATE`, hash))
	if err != nil {
		return QRLoginTicket{}, err
	}
	if ticket.State(at) == "expired" || ticket.ConsumedAt != nil {
		return QRLoginTicket{}, ErrForbidden
	}
	if ticket.ConfirmedAt != nil {
		if ticket.UserID == uid {
			return ticket, tx.Commit(ctx)
		}
		return QRLoginTicket{}, ErrConflict
	}
	ticket, err = scanQRLoginTicket(tx.QueryRow(ctx, `UPDATE im_qr_login_tickets SET user_id=$2,confirmed_at=$3 WHERE id=$1 RETURNING `+qrLoginTicketColumns, ticket.ID, uid, at))
	if err != nil {
		return QRLoginTicket{}, err
	}
	auditID, err := secureOpaqueToken("aud_qr_confirm_")
	if err != nil {
		return QRLoginTicket{}, err
	}
	metadata, _ := json.Marshal(map[string]any{"clientPlatform": ticket.ClientPlatform, "clientName": ticket.ClientName})
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,$2,'auth.qr_confirm','qr_login',$3,$4,$5)`, auditID, uid, ticket.ID, metadata, at); err != nil {
		return QRLoginTicket{}, err
	}
	return ticket, tx.Commit(ctx)
}

func (p *Postgres) ConsumeQRLoginTicket(ctx context.Context, id string, pollHash []byte, at time.Time) (QRLoginTicket, bool, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return QRLoginTicket{}, false, err
	}
	defer tx.Rollback(ctx)
	ticket, err := scanQRLoginTicket(tx.QueryRow(ctx, `SELECT `+qrLoginTicketColumns+` FROM im_qr_login_tickets WHERE id=$1 AND poll_token_hash=$2 FOR UPDATE`, id, pollHash))
	if err != nil {
		return QRLoginTicket{}, false, err
	}
	if ticket.State(at) != "confirmed" {
		return ticket, false, tx.Commit(ctx)
	}
	ticket, err = scanQRLoginTicket(tx.QueryRow(ctx, `UPDATE im_qr_login_tickets SET consumed_at=$2 WHERE id=$1 RETURNING `+qrLoginTicketColumns, id, at))
	if err != nil {
		return QRLoginTicket{}, false, err
	}
	auditID, err := secureOpaqueToken("aud_qr_consume_")
	if err != nil {
		return QRLoginTicket{}, false, err
	}
	metadata, _ := json.Marshal(map[string]any{"clientPlatform": ticket.ClientPlatform, "clientName": ticket.ClientName})
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,$2,'auth.qr_login','qr_login',$3,$4,$5)`, auditID, ticket.UserID, ticket.ID, metadata, at); err != nil {
		return QRLoginTicket{}, false, err
	}
	return ticket, true, tx.Commit(ctx)
}
