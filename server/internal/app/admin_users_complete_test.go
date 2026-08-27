package app

import (
	"context"
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
