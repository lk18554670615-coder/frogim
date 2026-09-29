-- 78: freeze notification provenance when it enters the enterprise outbox.
-- Never backfill old rows from today's ownership/credentials. Missing provenance
-- in an adopted/managed database is deliberately not deliverable.
ALTER TABLE im_push_outbox ADD COLUMN IF NOT EXISTS tenant_push jsonb;

CREATE OR REPLACE FUNCTION im_capture_tenant_push() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE realm record; target record; route jsonb; expiry timestamptz;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF NEW.tenant_push IS DISTINCT FROM OLD.tenant_push THEN
   RAISE EXCEPTION 'push provenance is immutable';
  END IF;
  RETURN NEW;
 END IF;
 -- Overwrite caller-supplied provenance, including on a standalone database.
 NEW.tenant_push := NULL;
 SELECT tenant_id,access_enabled,access_version INTO realm FROM im_tenant_identity WHERE singleton;
 IF NOT FOUND THEN RETURN NEW; END IF;
 SELECT platform_account_id,assignment_version,platform_auth_version INTO target
 FROM im_users WHERE id=NEW.user_id AND platform_account_id IS NOT NULL
 AND local_identity_state='active' AND NOT banned AND deleted_at IS NULL;
 IF NOT FOUND OR NOT realm.access_enabled THEN RETURN NEW; END IF;
 -- Leave headroom below the platform's 24h/60s contract limits for small
 -- database/service clock differences; retries still preserve the same expiry.
 expiry := NEW.created_at + interval '23 hours 59 minutes';
 route := '{}'::jsonb;
 IF NEW.event_type='message.created' THEN
  route := jsonb_build_object('conversationId',NEW.payload->'message'->>'conversationId',
    'messageId',NEW.payload->'message'->>'id','messageType',NEW.payload->'message'->>'type');
 ELSIF NEW.event_type='call.invited' THEN
  expiry := NEW.created_at + interval '45 seconds';
  SELECT LEAST(expiry,expires_at) INTO expiry FROM im_call_sessions WHERE id=NEW.payload->>'callId';
  IF expiry IS NULL THEN RETURN NEW; END IF;
  route := jsonb_build_object('conversationId',NEW.payload->>'conversationId',
    'callId',NEW.payload->>'callId','mediaType',NEW.payload->>'mediaType');
 END IF;
 -- MVCC captures one committed identity snapshot without introducing locks into
 -- business transactions. Concurrent revocation is safe: both dispatch sides
 -- recheck these exact versions; they never upgrade the snapshot on retry.
 NEW.tenant_push := route || jsonb_build_object('requestId','push_'||NEW.id::text,
  'accountId',target.platform_account_id,'tenantId',realm.tenant_id,'localUserId',NEW.user_id,
  'assignmentVersion',target.assignment_version,'authVersion',target.platform_auth_version,
  'realmVersion',realm.access_version,'eventType',NEW.event_type,'expiresAt',expiry);
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS im_push_provenance ON im_push_outbox;
CREATE TRIGGER im_push_provenance BEFORE INSERT OR UPDATE OF tenant_push ON im_push_outbox
 FOR EACH ROW EXECUTE FUNCTION im_capture_tenant_push();
