package telegrambackup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupDatabaseRoundTrip(t *testing.T) {
	database, err := openBackupDB(filepath.Join(t.TempDir(), "backup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.close()
	ctx := context.Background()
	backupID, err := database.create(ctx, "123", "测试聊天", "private")
	if err != nil {
		t.Fatal(err)
	}
	messages := []messageRecord{{
		MessageID:  7,
		SenderID:   "123",
		SenderName: "Alice",
		Date:       "2026-07-27T10:00:00Z",
		Text:       "hello",
		MediaType:  "photo",
		MediaID:    "photo.jpg",
	}}
	if err := database.addMessages(ctx, backupID, messages); err != nil {
		t.Fatal(err)
	}
	if err := database.updateCount(ctx, backupID); err != nil {
		t.Fatal(err)
	}
	document, err := database.document(ctx, backupID)
	if err != nil {
		t.Fatal(err)
	}
	if document.Backup.MessageCount != 1 ||
		len(document.Messages) != 1 ||
		document.Messages[0].Text != "hello" {
		t.Fatalf("document = %#v", document)
	}
	importedID, err := database.importDocument(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	if importedID == backupID {
		t.Fatal("import reused source backup ID")
	}
}

func TestCreateAndRestoreZip(t *testing.T) {
	directory := t.TempDir()
	archivePath := filepath.Join(directory, "backup.zip")
	document := exportDocument{
		Backup: backupRecord{
			ChatID:       "123",
			ChatTitle:    "Test",
			ChatType:     "private",
			MessageCount: 1,
		},
		Messages: []messageRecord{{MessageID: 1, Text: "hello"}},
		Version:  "2.0",
	}
	if err := createZip(
		archivePath,
		map[string]exportDocument{"backup.json": document},
		nil,
	); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(archivePath)
	if err != nil || info.Size() == 0 {
		t.Fatalf("archive stat = %v, %v", info, err)
	}
	database, err := openBackupDB(filepath.Join(directory, "restore.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.close()
	plugin := &Plugin{database: database}
	success, failed, err := plugin.restoreZip(context.Background(), archivePath)
	if err != nil || success != 1 || failed != 0 {
		t.Fatalf("restore = %d, %d, %v", success, failed, err)
	}
}

func TestSafeArchiveName(t *testing.T) {
	if got := safeArchiveName("../../evil"); got != "evil" {
		t.Fatalf("archive name = %q", got)
	}
	if got := safeArchiveName("中文"); got != "chat" {
		t.Fatalf("unicode archive name = %q", got)
	}
}

func TestSplitText(t *testing.T) {
	parts := splitText("1234567890", 4)
	if len(parts) != 3 {
		t.Fatalf("parts = %#v", parts)
	}
}
