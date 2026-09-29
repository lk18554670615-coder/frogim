package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/linli/im/server/internal/legacyimport"
	"golang.org/x/sys/windows"
)

func TestPrivateReportHasProtectedWindowsDACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	if saveReport(path, legacyimport.Report{ReadOnly: true}) != nil {
		t.Fatal("create report failed")
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal("read report security descriptor failed")
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("report inherited parent permissions")
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount != 2 {
		t.Fatal("unexpected report access rules")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal("current user unavailable")
	}
	sddl := sd.String()
	want := "D:P(A;;FA;;;SY)(A;;FA;;;" + user.User.Sid.String() + ")"
	if !strings.EqualFold(sddl, want) {
		t.Fatal("report is not restricted to current user and SYSTEM")
	}
}
