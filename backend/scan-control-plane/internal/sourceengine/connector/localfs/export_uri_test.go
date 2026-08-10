package localfs

import "testing"

func TestFileURIPathForWindowsDrive(t *testing.T) {
	got, err := fileURIPathForOS("file:///C:/Users/test/My%20Files/report.docx", "windows")
	if err != nil {
		t.Fatalf("fileURIPathForOS returned error: %v", err)
	}
	if want := `C:\Users\test\My Files\report.docx`; got != want {
		t.Fatalf("fileURIPathForOS = %q, want %q", got, want)
	}
}

func TestFileURIPathAcceptsLocalhost(t *testing.T) {
	got, err := fileURIPathForOS("file://localhost/tmp/report.docx", "linux")
	if err != nil {
		t.Fatalf("fileURIPathForOS returned error: %v", err)
	}
	if got != "/tmp/report.docx" {
		t.Fatalf("fileURIPathForOS = %q", got)
	}
}

func TestFileURIPathRejectsRemoteHost(t *testing.T) {
	if _, err := fileURIPathForOS("file://server/share/report.docx", "windows"); err == nil {
		t.Fatal("fileURIPathForOS accepted a remote file host")
	}
}
