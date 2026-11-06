package academic

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

// Paper-library business context ends here. Chat receives the same file inputs
// as a user upload, plus prose describing the task; no workflow-specific binding.
func paperChatAttachments(ctx context.Context, db *gorm.DB, user, conversation string) ([]map[string]any, string, error) {
	var handoff orm.AcademicHandoff
	if err := db.WithContext(ctx).Where("user_id=? AND conversation_id=? AND kind=?", user, conversation, "ppt").Take(&handoff).Error; err != nil {
		return nil, "", err
	}
	catalog, err := doc.NewDatasetCatalogService(doc.DatasetCatalogServiceDeps{DB: db})
	if err != nil {
		return nil, "", err
	}
	caller := doc.DatasetCatalogCaller{UserID: user}
	dataset, err := catalog.GetDatasetAccess(ctx, doc.DatasetGetRequest{UserID: user, DatasetID: handoff.DatasetID, Caller: caller})
	if err != nil {
		return nil, "", err
	}
	if dataset.Scene != "academic_papers" {
		return nil, "", errors.New("paper task dataset is unavailable")
	}
	var task PaperTaskInput
	if err = json.Unmarshal(handoff.InputJSON, &task); err != nil {
		return nil, "", err
	}
	if len(task.Papers) == 0 {
		return nil, "", errors.New("paper task has no selected papers")
	}
	files := make([]map[string]any, 0, len(task.Papers))
	var manifest strings.Builder
	paperNames := make(map[string]string, len(task.Papers))
	for index, paper := range task.Papers {
		source, contents, err := handoffSource(db.WithContext(ctx), handoff, paper.PaperID)
		if err != nil {
			return nil, "", err
		}
		data, extension, err := paperAttachmentBytes(ctx, db, user, source, contents)
		if err != nil {
			return nil, "", fmt.Errorf("prepare selected paper %s: %w", paper.PaperID, err)
		}
		// Position disambiguates equal titles and keeps the frozen selection order.
		filename := fmt.Sprintf("paper-%02d%s", index+1, extension)
		attachment, err := savePaperChatAttachment(ctx, db, user, handoff.ID, filename, data)
		if err != nil {
			return nil, "", err
		}
		files = append(files, attachment)
		title := firstNonEmpty(paper.Title, filename)
		paperNames[paper.PaperID] = filename + "（" + title + "）"
		fmt.Fprintf(&manifest, "- %s：%s", filename, title)
		if paper.SourceVersion != "" {
			fmt.Fprintf(&manifest, "，版本 %s", paper.SourceVersion)
		}
		if paper.PaperURL != "" {
			fmt.Fprintf(&manifest, "，来源 %s", paper.PaperURL)
		}
		if extension == ".md" {
			manifest.WriteString("（完整正文快照，无原 PDF 图像）")
		}
		manifest.WriteString("\n")
	}
	prompt := "\n\n请根据随消息附上的全部论文生成科研汇报 PPT。逐篇读全文，保留每篇论文及来源，不要以摘要代替全文。用户明确指定的视觉风格优先，否则采用科研风格。\n论文附件（参考材料）：\n" + manifest.String()
	instruction := strings.ToLower(task.UserInstruction)
	if !strings.Contains(instruction, "底图") && !strings.Contains(instruction, "背景") && !strings.Contains(instruction, "background") {
		prompt += "\n不生成 AI 底图。"
	}
	if handoff.ReflectionFirst {
		prompt += "\n先基于全部论文撰写并保存阅读心得，区分论文事实与个人见解，再结合心得和全部原论文生成 PPT。"
	}
	if len(task.Notes) > 0 {
		prompt += "\n已有阅读笔记（参考材料）："
		for _, note := range task.Notes {
			if name := paperNames[note.PaperID]; name != "" {
				prompt += fmt.Sprintf("\n\n%s · %s\n%s", name, note.Title, note.Content)
			}
		}
	}
	return files, prompt, nil
}

func paperAttachmentBytes(ctx context.Context, db *gorm.DB, user string, source paperSource, contents []orm.AcademicPaperContent) ([]byte, string, error) {
	if source.DocumentID != "" {
		document, err := sourceDocument(ctx, db, source)
		if err != nil {
			return nil, "", err
		}
		var ext struct {
			StoredPath string `json:"stored_path"`
		}
		if err = json.Unmarshal(document.Ext, &ext); err != nil && len(document.Ext) > 0 {
			return nil, "", err
		}
		if ext.StoredPath != "" {
			path, err := filepath.EvalSymlinks(ext.StoredPath)
			if err == nil {
				root, err := filepath.EvalSymlinks(doc.UploadRoot())
				if err != nil {
					return nil, "", err
				}
				rel, err := filepath.Rel(root, path)
				if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					return nil, "", errors.New("paper original is outside upload storage")
				}
				file, err := os.Open(path)
				if err != nil {
					return nil, "", err
				}
				defer file.Close()
				return readPaperAttachmentPDF(file, source.Hash)
			}
			if !errors.Is(err, os.ErrNotExist) {
				return nil, "", err
			}
		}
	}
	// Prefer the fulltext frozen at handoff creation. A newly prepared fulltext
	// may be used only when the selected source/version still matches.
	var content orm.AcademicPaperContent
	for _, candidate := range contents {
		if candidate.PaperID != source.Paper.PaperID {
			continue
		}
		// Snapshot JSON intentionally omits private storage and page data.
		// Reload the exact frozen row, then verify its content identity.
		if err := db.WithContext(ctx).Where("id=? AND user_id=? AND paper_id=?", candidate.ID, user, source.Paper.PaperID).Take(&content).Error; err != nil {
			return nil, "", err
		}
		if (candidate.ContentHash != "" && content.ContentHash != candidate.ContentHash) || (candidate.SourceVersion != "" && content.SourceVersion != candidate.SourceVersion) {
			return nil, "", errors.New("frozen paper full text changed")
		}
		break
	}
	if content.ID == "" {
		var err error
		content, err = findSourceContent(db.WithContext(ctx), user, source)
		if err != nil {
			return nil, "", err
		}
	}
	if content.SourceKey != source.key() && !(content.SourceKind == "external" && source.Paper.SourceVersion != "" && content.SourceVersion == source.Paper.SourceVersion && content.SourceProvider == source.Paper.SourceProvider) {
		return nil, "", errors.New("frozen paper source changed")
	}
	if !usablePaperContent(content) {
		return nil, "", errors.New("selected paper full text is not ready")
	}
	digest := sha256.Sum256(content.PagesJSON)
	if hex.EncodeToString(digest[:]) != content.ContentHash {
		return nil, "", errors.New("frozen paper full text changed")
	}
	acquisition := paperContentAcquisition(content.QualityJSON)
	if acquisition.FileRef != "" {
		originals, err := doc.NewOriginalService(doc.OriginalServiceDeps{DB: db})
		if err != nil {
			return nil, "", err
		}
		if file, err := originals.OpenOriginal(ctx, doc.DatasetCatalogCaller{UserID: user}, acquisition.FileRef); err == nil {
			defer file.Close()
			return readPaperAttachmentPDF(file, content.SourceHash)
		}
	}
	var pages []ContentPage
	if err := json.Unmarshal(content.PagesJSON, &pages); err != nil {
		return nil, "", err
	}
	var text strings.Builder
	fmt.Fprintf(&text, "# %s\n\nSource metadata:\n%s\n\nContent hash: %s\n\n", source.Paper.Title, marshal(source.Paper), content.ContentHash)
	readable := false
	for _, page := range pages {
		if strings.TrimSpace(page.Text) != "" {
			readable = true
		}
		fmt.Fprintf(&text, "## Page %d\n\n%s\n\n", page.Page, page.Text)
	}
	if !readable {
		return nil, "", errors.New("selected paper full text has no readable pages")
	}
	return []byte(text.String()), ".md", nil
}

func readPaperAttachmentPDF(file *os.File, expectedHash string) ([]byte, string, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, "", err
	}
	if !info.Mode().IsRegular() {
		return nil, "", errors.New("paper original is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, doc.OriginalFileMaxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > doc.OriginalFileMaxBytes || !bytes.HasPrefix(data, []byte("%PDF-")) {
		return nil, "", errors.New("paper original is not a supported PDF")
	}
	hash := sha256.Sum256(data)
	if expectedHash != "" && hex.EncodeToString(hash[:]) != expectedHash {
		return nil, "", errors.New("selected paper PDF changed")
	}
	return data, ".pdf", nil
}

func savePaperChatAttachment(ctx context.Context, db *gorm.DB, user, handoff, filename string, data []byte) (map[string]any, error) {
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	identity := sha256.Sum256(marshal([]string{user, handoff, filename, digest}))
	id := hex.EncodeToString(identity[:])
	directory := filepath.Join(doc.TempUserFilesRoot(user), id)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(directory, filename)
	// Atomic publication also makes a retry after interruption safe.
	temporary, err := os.CreateTemp(directory, ".attachment-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(temporary.Name())
	if _, err = temporary.Write(data); err != nil {
		temporary.Close()
		return nil, err
	}
	if err = temporary.Close(); err != nil {
		return nil, err
	}
	if err = os.Rename(temporary.Name(), path); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	mime := "application/pdf"
	if filepath.Ext(filename) == ".md" {
		mime = "text/markdown"
	}
	row := orm.UploadedFile{UploadFileID: id, ContentHash: digest, Status: doc.UploadedFileStateUploaded,
		Ext:       marshal(map[string]any{"stored_path": path, "stored_name": filename, "original_filename": filename, "file_size": len(data), "content_type": mime}),
		BaseModel: orm.BaseModel{CreateUserID: user, CreatedAt: now, UpdatedAt: now}}
	if err = db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return nil, err
	}
	return map[string]any{"input_type": "file", "uri": path, "filename": filename, "name": filename}, nil
}
