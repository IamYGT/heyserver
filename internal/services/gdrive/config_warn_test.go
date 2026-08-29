package gdrive

import "testing"

func TestWarnConfig_noPanicWhenUnsetRedirect(t *testing.T) {
	WarnConfig("client-id", "")
}

func TestWarnConfig_skipsWhenNoClient(t *testing.T) {
	WarnConfig("", "https://example/cb")
}
