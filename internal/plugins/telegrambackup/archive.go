package telegrambackup

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxRestoreBytes     = 512 << 20
	maxJSONBytes        = 64 << 20
	maxArchiveDocuments = 1000
)

func (p *Plugin) exportOne(
	ctx context.Context,
	backupID int64,
) (string, backupRecord, error) {
	document, err := p.database.document(ctx, backupID)
	if err != nil {
		return "", backupRecord{}, err
	}
	jobDir, err := os.MkdirTemp(p.workDir, "export-*")
	if err != nil {
		return "", backupRecord{}, err
	}
	fileName := "backup_" + safeArchiveName(document.Backup.ChatTitle) +
		"_" + time.Now().UTC().Format("20060102T150405Z") + ".zip"
	outputPath := filepath.Join(jobDir, fileName)
	if err := createZip(outputPath, map[string]exportDocument{
		"backup.json": document,
	}, nil); err != nil {
		os.RemoveAll(jobDir)
		return "", backupRecord{}, err
	}
	return outputPath, document.Backup, nil
}

func (p *Plugin) exportAll(
	ctx context.Context,
) (string, int, int, error) {
	backups, err := p.database.list(ctx)
	if err != nil {
		return "", 0, 0, err
	}
	if len(backups) == 0 {
		return "", 0, 0, errors.New("没有可导出的备份")
	}
	documents := make(map[string]exportDocument, len(backups))
	totalMessages := 0
	for _, backup := range backups {
		document, err := p.database.document(ctx, backup.ID)
		if err != nil {
			return "", 0, 0, err
		}
		totalMessages += backup.MessageCount
		name := fmt.Sprintf(
			"backup_%d_%s/backup.json",
			backup.ID,
			safeArchiveName(backup.ChatTitle),
		)
		documents[name] = document
	}
	links, err := p.database.links(ctx, "")
	if err != nil {
		return "", 0, 0, err
	}
	jobDir, err := os.MkdirTemp(p.workDir, "export-all-*")
	if err != nil {
		return "", 0, 0, err
	}
	outputPath := filepath.Join(
		jobDir,
		"telegram_backups_"+time.Now().UTC().Format("20060102T150405Z")+".zip",
	)
	if err := createZip(outputPath, documents, links); err != nil {
		os.RemoveAll(jobDir)
		return "", 0, 0, err
	}
	return outputPath, len(backups), totalMessages, nil
}

func createZip(
	outputPath string,
	documents map[string]exportDocument,
	links []chatLink,
) error {
	output, err := os.OpenFile(
		outputPath,
		os.O_CREATE|os.O_WRONLY|os.O_EXCL,
		0o600,
	)
	if err != nil {
		return err
	}
	archive := zip.NewWriter(output)
	closeWithError := func(inputErr error) error {
		archiveErr := archive.Close()
		fileErr := output.Close()
		return errors.Join(inputErr, archiveErr, fileErr)
	}
	for name, document := range documents {
		writer, err := archive.CreateHeader(&zip.FileHeader{
			Name:     name,
			Method:   zip.Deflate,
			Modified: time.Now().UTC(),
		})
		if err != nil {
			return closeWithError(err)
		}
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(document); err != nil {
			return closeWithError(err)
		}
	}
	if len(links) > 0 {
		writer, err := archive.CreateHeader(&zip.FileHeader{
			Name:     "chat_links.json",
			Method:   zip.Deflate,
			Modified: time.Now().UTC(),
		})
		if err != nil {
			return closeWithError(err)
		}
		if err := json.NewEncoder(writer).Encode(links); err != nil {
			return closeWithError(err)
		}
	}
	return closeWithError(nil)
}

func (p *Plugin) restoreArchive(
	ctx context.Context,
	archivePath string,
) (success, failed int, err error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return 0, 0, err
	}
	header := make([]byte, 4)
	count, readErr := io.ReadFull(file, header)
	file.Close()
	if readErr != nil && readErr != io.ErrUnexpectedEOF {
		return 0, 0, readErr
	}
	if count >= 2 && header[0] == 'P' && header[1] == 'K' {
		return p.restoreZip(ctx, archivePath)
	}
	document, err := readDocumentFile(archivePath)
	if err != nil {
		return 0, 1, err
	}
	if _, err := p.database.importDocument(ctx, document); err != nil {
		return 0, 1, err
	}
	return 1, 0, nil
}

func (p *Plugin) restoreZip(
	ctx context.Context,
	archivePath string,
) (success, failed int, err error) {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return 0, 0, err
	}
	defer archive.Close()
	if len(archive.File) > maxArchiveDocuments+1 {
		return 0, 0, errors.New("ZIP 文件条目过多")
	}
	var totalUncompressed uint64
	for _, entry := range archive.File {
		clean := path.Clean(strings.ReplaceAll(entry.Name, "\\", "/"))
		if clean == "." || strings.HasPrefix(clean, "../") ||
			strings.HasPrefix(clean, "/") || entry.FileInfo().IsDir() {
			continue
		}
		totalUncompressed += entry.UncompressedSize64
		if totalUncompressed > maxRestoreBytes {
			return success, failed, errors.New("ZIP 解压后数据超过 512 MiB")
		}
		if path.Base(clean) == "chat_links.json" {
			links, linkErr := readLinksEntry(entry)
			if linkErr != nil {
				failed++
				continue
			}
			for _, link := range links {
				if saveErr := p.database.saveLink(ctx, link); saveErr != nil {
					failed++
				}
			}
			continue
		}
		if path.Base(clean) != "backup.json" {
			continue
		}
		if entry.UncompressedSize64 > maxJSONBytes {
			failed++
			continue
		}
		document, documentErr := readDocumentEntry(entry)
		if documentErr != nil {
			failed++
			continue
		}
		if _, importErr := p.database.importDocument(ctx, document); importErr != nil {
			failed++
			continue
		}
		success++
	}
	if success == 0 && failed == 0 {
		return 0, 0, errors.New("ZIP 中未找到 backup.json")
	}
	return success, failed, nil
}

func readDocumentFile(filePath string) (exportDocument, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return exportDocument{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return exportDocument{}, err
	}
	if info.Size() > maxJSONBytes {
		return exportDocument{}, errors.New("JSON 备份超过 64 MiB")
	}
	return decodeDocument(io.LimitReader(file, maxJSONBytes+1))
}

func readDocumentEntry(entry *zip.File) (exportDocument, error) {
	reader, err := entry.Open()
	if err != nil {
		return exportDocument{}, err
	}
	defer reader.Close()
	return decodeDocument(io.LimitReader(reader, maxJSONBytes+1))
}

func decodeDocument(reader io.Reader) (exportDocument, error) {
	var document exportDocument
	if err := json.NewDecoder(reader).Decode(&document); err != nil {
		return exportDocument{}, err
	}
	if document.Version == "" {
		document.Version = "1.0"
	}
	if document.Backup.MessageCount == 0 {
		document.Backup.MessageCount = len(document.Messages)
	}
	return document, nil
}

func readLinksEntry(entry *zip.File) ([]chatLink, error) {
	if entry.UncompressedSize64 > maxJSONBytes {
		return nil, errors.New("链接备份超过 64 MiB")
	}
	reader, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	var links []chatLink
	err = json.NewDecoder(io.LimitReader(reader, maxJSONBytes+1)).Decode(&links)
	return links, err
}

func safeArchiveName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "chat"
	}
	var result strings.Builder
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' {
			result.WriteRune(character)
		} else {
			result.WriteRune('_')
		}
		if result.Len() >= 80 {
			break
		}
	}
	cleaned := strings.Trim(result.String(), "_")
	if cleaned == "" {
		return "chat"
	}
	return cleaned
}

type boundedWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *boundedWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.remaining {
		return 0, errors.New("恢复文件超过 512 MiB")
	}
	count, err := w.writer.Write(data)
	w.remaining -= int64(count)
	return count, err
}

func documentBytes(document exportDocument) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
