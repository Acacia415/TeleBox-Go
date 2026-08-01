package managedtool

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReceiptAndQuarantine(t *testing.T) {
	root := t.TempDir()
	tool := filepath.Join(root, "tool")
	if err := os.WriteFile(tool, []byte("managed tool"), 0o700); err != nil {
		t.Fatal(err)
	}
	digest, err := FileSHA256(tool)
	if err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(root, "tool.receipt.json")
	if err := WriteReceipt(receipt, "https://example.invalid/tool", "1", digest); err != nil {
		t.Fatal(err)
	}
	if err := Verify(tool, receipt); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tool, []byte("changed"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Verify(tool, receipt); err == nil {
		t.Fatal("Verify accepted a changed managed tool")
	}
	target, err := Quarantine(tool, filepath.Join(root, "quarantine"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 != 0 {
		t.Fatalf("quarantined mode = %v", info.Mode())
	}
}
