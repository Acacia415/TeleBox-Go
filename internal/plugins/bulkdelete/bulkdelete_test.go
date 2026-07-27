package bulkdelete

import (
	"strings"
	"testing"
)

func TestHelpTextMode(t *testing.T) {
	if got := helpText(".", true); !strings.Contains(got, "当前删除他人权限：开启") {
		t.Fatalf("enabled help = %q", got)
	}
	if got := helpText("!", false); !strings.Contains(got, "!bd") ||
		!strings.Contains(got, "当前删除他人权限：关闭") {
		t.Fatalf("disabled help = %q", got)
	}
}
