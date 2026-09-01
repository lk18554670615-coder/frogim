package app

import (
	"context"
	"sync"
	"testing"

	"github.com/linli/im/server/internal/teststore"
)

func TestCreateAdminUserRequiresMainlandPhoneAndKeepsDynamicPasswordPolicy(t *testing.T) {
	a, err := New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	created, err := a.CreateAdminUser(context.Background(), "admin-1", "+8613800138000", "后台创建用户", "StrongPass123!", "female", "运营工单 USER-1")
	if err != nil {
		t.Fatal(err)
	}
	if created.Phone != "13800138000" || created.Gender != "female" || created.Handle == "" {
		t.Fatalf("created user=%+v", created)
	}
	if _, err = a.CreateAdminUser(context.Background(), "admin-1", "13800138000", "重复号码", "StrongPass123!", "female", "重复测试"); err != ErrConflict {
		t.Fatalf("duplicate phone error=%v", err)
	}
	for _, phone := range []string{"+85291234567", "12800138000", "1380013800"} {
		if _, err = a.CreateAdminUser(context.Background(), "admin-1", phone, "无效号码", "StrongPass123!", "unspecified", "校验测试"); err != ErrInvalid {
			t.Fatalf("phone %q error=%v", phone, err)
		}
	}
	if _, err = a.CreateAdminUser(context.Background(), "admin-1", "13900139000", "短密码", "short", "male", "密码策略测试"); err != ErrInvalid {
		t.Fatalf("short password error=%v", err)
	}
}

func TestCreateAdminUsersBatchKeepsOrderAndContinuesAfterRowFailures(t *testing.T) {
	a, err := New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	inputs := []AdminUserBatchInput{
		{ClientRow: 2, Phone: "+8613811110001", Name: "批量用户一", Password: "StrongPass123!", Gender: "female"},
		{ClientRow: 3, Phone: "12811110002", Name: "错误号码", Password: "StrongPass123!", Gender: "male"},
		{ClientRow: 4, Phone: "13811110001", Name: "文件内重复", Password: "StrongPass123!", Gender: "female"},
		{ClientRow: 5, Phone: "13911110003", Name: "批量用户二", Password: "AnotherPass123!", Gender: "unspecified"},
	}
	batchID, results, err := a.CreateAdminUsersBatch(context.Background(), "admin-1", inputs, "运营工单 BATCH-1")
	if err != nil {
		t.Fatal(err)
	}
	if batchID == "" || len(results) != len(inputs) {
		t.Fatalf("batch=%q results=%v", batchID, results)
	}
	wantStatus := []string{"failed", "failed", "failed", "created"}
	wantCode := []string{"DUPLICATE_IN_FILE", "INVALID_PHONE", "DUPLICATE_IN_FILE", ""}
	for index := range results {
		if results[index].ClientRow != inputs[index].ClientRow || results[index].Status != wantStatus[index] || results[index].Code != wantCode[index] {
			t.Fatalf("result[%d]=%+v", index, results[index])
		}
	}
	if results[0].User != nil || results[2].User != nil || results[3].User == nil || results[3].User.Phone != "13911110003" {
		t.Fatalf("created users=%+v %+v %+v", results[0].User, results[2].User, results[3].User)
	}

	_, repeated, err := a.CreateAdminUsersBatch(context.Background(), "admin-1", []AdminUserBatchInput{
		{ClientRow: 2, Phone: "13911110003", Name: "不能覆盖", Password: "StrongPass123!", Gender: "female"},
	}, "重复导入")
	if err != nil || len(repeated) != 1 || repeated[0].Code != "PHONE_ALREADY_EXISTS" {
		t.Fatalf("repeated=%+v err=%v", repeated, err)
	}
	user, err := a.User(results[3].User.ID)
	if err != nil || user.Name != "批量用户二" {
		t.Fatalf("existing user was changed: user=%+v err=%v", user, err)
	}
}

func TestCreateAdminUsersBatchRejectsInvalidEnvelope(t *testing.T) {
	a, err := New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.CreateAdminUsersBatch(context.Background(), "admin-1", nil, "reason"); err != ErrInvalid {
		t.Fatalf("empty batch error=%v", err)
	}
	tooMany := make([]AdminUserBatchInput, maxAdminUserBatchSize+1)
	if _, _, err = a.CreateAdminUsersBatch(context.Background(), "admin-1", tooMany, "reason"); err != ErrInvalid {
		t.Fatalf("oversized batch error=%v", err)
	}
}

func TestCreateAdminUsersBatchConcurrentPhoneConflictCreatesOnlyOnce(t *testing.T) {
	a, err := New(context.Background(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	results := make([][]AdminUserBatchItemResult, 2)
	errs := make([]error, len(results))
	var wait sync.WaitGroup
	wait.Add(len(results))
	for index := range results {
		go func() {
			defer wait.Done()
			_, results[index], errs[index] = a.CreateAdminUsersBatch(context.Background(), "admin-1", []AdminUserBatchInput{{
				ClientRow: index + 2, Phone: "13711110009", Name: "并发用户", Password: "StrongPass123!", Gender: "unspecified",
			}}, "并发重复校验")
		}()
	}
	wait.Wait()
	for _, batchErr := range errs {
		if batchErr != nil {
			t.Fatal(batchErr)
		}
	}
	created, conflicts := 0, 0
	for _, batch := range results {
		if len(batch) != 1 {
			t.Fatalf("batch=%+v", batch)
		}
		if batch[0].Status == "created" {
			created++
		} else if batch[0].Code == "PHONE_ALREADY_EXISTS" {
			conflicts++
		}
	}
	if created != 1 || conflicts != 1 {
		t.Fatalf("created=%d conflicts=%d results=%+v", created, conflicts, results)
	}
}
