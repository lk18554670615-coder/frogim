package privatefile

import (
	"golang.org/x/sys/windows"
	"os"
	"unsafe"
)

// Create never replaces an existing file. Windows 0600 is not a DACL: protect
// private release artifacts from inherited read access by other local users.
func Create(path string) (*os.File, error) {
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return nil, e
	}
	d, e := windows.SecurityDescriptorFromString("D:P(A;;FA;;;SY)(A;;FA;;;" + user.User.Sid.String() + ")")
	if e != nil {
		return nil, e
	}
	a := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: d}
	name, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return nil, e
	}
	h, e := windows.CreateFile(name, windows.GENERIC_WRITE, 0, a, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		return nil, e
	}
	return os.NewFile(uintptr(h), path), nil
}
