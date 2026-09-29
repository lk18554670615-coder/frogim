package privatefile

import (
	"golang.org/x/sys/windows"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateWindowsDACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bundle.json")
	f, e := Create(path)
	if e != nil {
		t.Fatal(e)
	}
	f.Close()
	d, e := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		t.Fatal(e)
	}
	s := d.String()
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(s, "D:P") || !strings.Contains(s, "(A;;FA;;;SY)") || !strings.Contains(s, "(A;;FA;;;"+user.User.Sid.String()+")") || strings.Contains(s, ";;;BU)") || strings.Contains(s, ";;;WD)") {
		t.Fatal("private artifact inherited broad read access")
	}
}
