package documentdirectory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	"lazymind/core/store"
)

type MailAttachment struct {
	Filename string `json:"filename"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size"`
}

type DirectoryMail struct {
	Subject     string           `json:"subject"`
	From        string           `json:"from"`
	To          string           `json:"to"`
	CC          string           `json:"cc"`
	Date        string           `json:"date"`
	Mailbox     string           `json:"mailbox"`
	Body        string           `json:"body"`
	Attachments []MailAttachment `json:"attachments"`
	Truncated   bool             `json:"truncated"`
}

var mailRefPattern = regexp.MustCompile(`^.+::[1-9][0-9]*$`)
var messageIDPattern = regexp.MustCompile(`^<[^<>\r\n]+>$`)

// Load only an owned live reference; reading must not touch directory metadata.
func mailLocator(ctx context.Context, db *gorm.DB, uid, directoryID, entryID string) (map[string]string, error) {
	var directory orm.DocumentDirectory
	if err := db.WithContext(ctx).Where("id=? AND user_id=? AND deleted_at IS NULL AND ready=?", directoryID, uid, true).Take(&directory).Error; err != nil {
		return nil, err
	}
	var entry orm.DocumentDirectoryEntry
	if err := db.WithContext(ctx).Where("id=? AND directory_id=? AND excluded=? AND source=?", entryID, directoryID, false, "mail").Take(&entry).Error; err != nil {
		return nil, err
	}
	var locator map[string]any
	if json.Unmarshal(entry.LocatorJSON, &locator) != nil {
		return nil, errors.New("mail_locator_missing")
	}
	value := func(key string) string { s, _ := locator[key].(string); return strings.TrimSpace(s) }
	mailbox, ref, messageID := value("mailbox"), value("id"), value("thread_id")
	if messageID == "" {
		messageID = entry.ResourceID
	}
	if mailbox == "" || !strings.EqualFold(mailbox, entry.Namespace) || !mailRefPattern.MatchString(ref) || !messageIDPattern.MatchString(messageID) || (value("folder") != "" && !strings.HasPrefix(ref, value("folder")+"::")) {
		return nil, errors.New("mail_locator_missing")
	}
	return map[string]string{"email": mailbox, "message_ref": ref, "message_id": messageID}, nil
}

func selectMailCredential(items []string, mailbox string) (map[string]string, error) {
	var selected map[string]string
	for _, item := range items {
		var credential map[string]string
		if json.Unmarshal([]byte(item), &credential) != nil || !strings.EqualFold(strings.TrimSpace(credential["email"]), mailbox) {
			continue
		}
		status := strings.ToUpper(strings.TrimSpace(credential["status"]))
		if (status != "" && status != "ACTIVE") || credential["secret"] == "" {
			continue
		}
		if selected != nil {
			return nil, errors.New("mail_unavailable")
		}
		selected = credential
	}
	if selected == nil {
		return nil, errors.New("mail_unavailable")
	}
	return selected, nil
}

func ReadMailHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	uid := store.UserID(r)
	input, err := mailLocator(r.Context(), store.DB(), uid, common.PathVar(r, "directory_id"), common.PathVar(r, "entry_id"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			reply(w, nil, err)
		} else {
			mailError(w, err.Error())
		}
		return
	}
	credentials, err := modelconfig.LoadMailToolConfig(r.Context(), uid)
	if err != nil {
		mailError(w, "mail_read_failed")
		return
	}
	credential, err := selectMailCredential(credentials, input["email"])
	if err != nil {
		mailError(w, err.Error())
		return
	}
	input["provider"], input["secret"] = credential["provider"], credential["secret"]
	var result struct {
		Mail      *DirectoryMail `json:"mail"`
		ErrorCode string         `json:"error_code"`
	}
	headers := map[string]string{"X-LazyMind-Internal-Token": os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN")}
	err = common.ApiPost(r.Context(), common.JoinURL(common.ChatServiceEndpoint(), "/api/mail/read"), input, headers, &result, 35*time.Second)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			mailError(w, "mail_timeout")
		} else {
			mailError(w, "mail_read_failed")
		}
		return
	}
	if result.ErrorCode != "" {
		mailError(w, result.ErrorCode)
		return
	}
	if result.Mail == nil {
		mailError(w, "mail_read_failed")
		return
	}
	common.ReplyOK(w, result.Mail)
}

func mailError(w http.ResponseWriter, code string) {
	status := http.StatusBadGateway
	switch code {
	case "mail_unavailable", "mail_authorization_failed":
		status = http.StatusConflict
	case "mail_locator_missing", "mail_location_changed":
		status = http.StatusNotFound
	case "mail_timeout":
		status = http.StatusGatewayTimeout
	default:
		code = "mail_read_failed"
	}
	common.ReplyErr(w, code, status)
}
