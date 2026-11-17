package executor

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
	"golang.org/x/net/html"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/artifactfile"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/graphengine"
)

const (
	MaxPPTImageBytes = 12 << 20
	maxPPTHTMLBytes  = 16 << 20
	pptImageMarker   = "<!-- ppt-material-images:"
	pptImageAttr     = "data-ppt-image"
)

func UsesPPTImageContract(attempt AttemptContext) bool {
	return attempt.Execution != nil && attempt.Execution.OutputValidator == graphengine.PPTImagesOutputValidator
}

// PPTImageMIME verifies the raster payload rather than trusting an extension or
// HTTP header. Bound the dimensions before decoding untrusted image bytes.
func PPTImageMIME(data []byte) (string, error) {
	if len(data) == 0 || len(data) > MaxPPTImageBytes {
		return "", errors.New("image is empty or exceeds 12 MiB")
	}
	reader := bytes.NewReader(data)
	config, format, err := image.DecodeConfig(reader)
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 36_000_000/config.Height {
		return "", errors.New("image format or dimensions are invalid; use PNG, JPEG, GIF, WebP or BMP up to 36 megapixels")
	}
	switch format {
	case "png", "jpeg", "gif", "webp", "bmp":
	default:
		return "", errors.New("use PNG, JPEG, GIF, WebP or BMP for a PPT image")
	}
	reader.Reset(data)
	if _, _, err := image.Decode(reader); err != nil {
		return "", errors.New("image is corrupt or truncated")
	}
	return "image/" + format, nil
}

// External image publications must carry the actual bytes. Store the decoded
// MIME so later reads can render even when the upstream filename was misleading.
func normalizePPTImageUpload(slot string, raw json.RawMessage) (json.RawMessage, error) {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil || value["storage"] != "inline_base64" {
		return nil, controlstore.Reject("INVALID_ARTIFACT", slot+" requires image bytes. Download the image and publish it using output.local_path.")
	}
	encoded, _ := value["content_base64"].(string)
	if len(encoded) > base64.StdEncoding.EncodedLen(MaxPPTImageBytes) {
		return nil, controlstore.Reject("INVALID_ARTIFACT", slot+": image exceeds 12 MiB")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil, controlstore.Reject("INVALID_ARTIFACT", slot+": invalid image file encoding")
	}
	mime, err := PPTImageMIME(data)
	if err != nil {
		return nil, controlstore.Reject("INVALID_ARTIFACT", slot+": "+err.Error())
	}
	if slot == "background_images" {
		config, _, _ := image.DecodeConfig(bytes.NewReader(data))
		if config.Width != 1280 || config.Height != 720 {
			return nil, controlstore.Reject("INVALID_ARTIFACT", "AI background must be normalized to 1280×720 before publication")
		}
	}
	value["mime_type"] = mime
	return json.Marshal(value)
}

type pptInput struct {
	RevisionID string          `json:"source_revision_id"`
	SortOrder  int             `json:"sort_order"`
	Value      json.RawMessage `json:"value"`
}

func pptInputs(attempt AttemptContext, slot string) ([]pptInput, error) {
	value := attempt.Inputs[slot]
	if value == nil {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if raw[0] == '{' {
		raw = append(append([]byte{'['}, raw...), ']')
	}
	var items []pptInput
	err = json.Unmarshal(raw, &items)
	return items, err
}

func pptInputText(db *gorm.DB, attempt AttemptContext, slot string, input pptInput) (string, error) {
	raw := input.Value
	if len(raw) == 0 {
		var revision orm.WorkflowSlotRevision
		if err := db.Where("id = ? AND session_id = ? AND slot_id = ?", input.RevisionID, attempt.SessionID, slot).Take(&revision).Error; err != nil {
			return "", err
		}
		resolved, err := controlstore.ResolveValue(db, revision)
		if err != nil {
			return "", err
		}
		raw = resolved
	}
	return pptText(raw)
}

func pptText(raw json.RawMessage) (string, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text), nil
	}
	var body struct {
		Text    string `json:"text"`
		Data    string `json:"data"`
		Storage string `json:"storage"`
		Type    string `json:"type"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", err
	}
	if body.Storage == "managed_file" && body.Type == "text" {
		value, err := artifactfile.Inline(raw)
		if err != nil {
			return "", err
		}
		var file struct {
			Base64 string `json:"content_base64"`
		}
		if err := json.Unmarshal(value, &file); err != nil {
			return "", err
		}
		data, err := base64.StdEncoding.Strict().DecodeString(file.Base64)
		return strings.TrimSpace(string(data)), err
	}
	if body.Text == "" {
		body.Text = body.Data
	}
	return strings.TrimSpace(body.Text), nil
}

func pptOption(db *gorm.DB, attempt AttemptContext, slot string) (string, error) {
	inputs, err := pptInputs(attempt, slot)
	if err != nil || len(inputs) == 0 {
		return "", err
	}
	return pptInputText(db, attempt, slot, inputs[0])
}

func pptMaterialIDs(brief string, approved []pptInput) ([]string, error) {
	allowed := make(map[string]bool, len(approved))
	for _, input := range approved {
		allowed[input.RevisionID] = true
	}
	var ids []string
	for _, line := range strings.Split(brief, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, pptImageMarker) {
			continue
		}
		if ids != nil || !strings.HasSuffix(line, "-->") ||
			json.Unmarshal([]byte(strings.TrimSpace(line[len(pptImageMarker):len(line)-3])), &ids) != nil || ids == nil {
			return nil, errors.New("use one ppt-material-images comment with a JSON array of artifact revision IDs")
		}
		seen := make(map[string]bool, len(ids))
		for _, id := range ids {
			if id == "" || !allowed[id] || seen[id] {
				return nil, errors.New("each PPT material ID must be a distinct bound material_images revision")
			}
			seen[id] = true
		}
	}
	if ids == nil {
		return nil, errors.New("add a ppt-material-images comment to the approved page prompt; use [] for a page without material images")
	}
	return ids, nil
}

// PPTPageImages is built from the attempt's frozen inputs. Only the lightweight
// brief and the approved IDs reach the model.
type PPTPageImages struct {
	sessionID  string
	page       int
	brief      string
	materials  []string
	background string
}

func LoadPPTPageImages(db *gorm.DB, attempt AttemptContext, page int) (PPTPageImages, error) {
	result := PPTPageImages{sessionID: attempt.SessionID, page: page}
	outlines, err := pptInputs(attempt, "slide_outline")
	if err != nil {
		return result, err
	}
	for _, input := range outlines {
		if input.SortOrder == page && page > 0 {
			result.brief, err = pptInputText(db, attempt, "slide_outline", input)
			if err != nil {
				return result, fmt.Errorf("page %d: the approved slide_outline could not be read", page)
			}
			break
		}
	}
	if result.brief == "" {
		return result, fmt.Errorf("page %d has no approved slide_outline", page)
	}
	approved, err := pptInputs(attempt, "material_images")
	if err != nil {
		return result, err
	}
	result.materials, err = pptMaterialIDs(result.brief, approved)
	if err != nil {
		return result, fmt.Errorf("page %d: %w", page, err)
	}
	backgrounds, err := pptInputs(attempt, "background_images")
	if err != nil {
		return result, err
	}
	enabled, err := pptOption(db, attempt, "generate_background_images")
	if err != nil {
		return result, errors.New("the AI background choice could not be read")
	}
	if enabled == "disabled" {
		return result, nil
	}
	for _, input := range backgrounds {
		if input.SortOrder == page {
			result.background = input.RevisionID
			break
		}
	}
	if (enabled == "enabled" || len(backgrounds) > 0) && len(backgrounds) != len(outlines) {
		return result, fmt.Errorf("expected %d saved AI backgrounds, got %d; recover the background step", len(outlines), len(backgrounds))
	}
	if (enabled == "enabled" || len(backgrounds) > 0) && result.background == "" {
		return result, fmt.Errorf("page %d has no saved AI background; retry the corresponding background page", page)
	}
	return result, nil
}

func (p PPTPageImages) GenerationBrief() string {
	brief := p.brief + "\n\nManaged PPT image bindings for this page:"
	if len(p.materials) == 0 {
		brief += "\nNo bound foreground photos."
	}
	for _, id := range p.materials {
		brief += fmt.Sprintf("\nPlace an actual foreground <img %s=%q> at the position described in Assets. Supply alt text and layout attributes; Core supplies src.", pptImageAttr, id)
	}
	if p.background != "" {
		brief += "\nInclude the #bg layer. Core supplies this page's bound AI background as its CSS background-image."
	}
	return brief
}

func pptImageURI(db *gorm.DB, session, slot, id string) (string, error) {
	var row orm.WorkflowSlotRevision
	if err := db.Where("id = ? AND session_id = ? AND slot_id = ?", id, session, slot).Take(&row).Error; err != nil {
		return "", err
	}
	value, err := controlstore.ResolveValue(db, row)
	if err != nil {
		return "", err
	}
	var file struct {
		Storage string `json:"storage"`
		MIME    string `json:"mime_type"`
		Base64  string `json:"content_base64"`
	}
	if json.Unmarshal(value, &file) != nil || file.Storage != "managed_file" {
		return "", errors.New("the bound revision has no saved image file")
	}
	value, err = artifactfile.Inline(value)
	if err != nil {
		return "", err
	}
	if json.Unmarshal(value, &file) != nil || file.Storage != "inline_base64" || file.Base64 == "" {
		return "", errors.New("the bound revision has no saved image bytes")
	}
	switch file.MIME {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp":
		return "data:" + file.MIME + ";base64," + file.Base64, nil
	default:
		return "", errors.New("the bound revision has an unsupported image MIME type")
	}
}

func pptAttr(node *html.Node, name string) string {
	for _, attr := range node.Attr {
		if attr.Namespace == "" && attr.Key == name {
			return attr.Val
		}
	}
	return ""
}

func setPPTAttr(node *html.Node, name, value string) {
	for i, attr := range node.Attr {
		if attr.Namespace == "" && attr.Key == name {
			node.Attr[i].Val = value
			return
		}
	}
	node.Attr = append(node.Attr, html.Attribute{Key: name, Val: value})
}

// Only count live elements in the slide body. A placeholder in comments,
// templates, SVG, fallback markup or the background layer cannot bind a photo.
func pptBodyElement(node *html.Node, foreground bool) bool {
	for parent := node; parent != nil; parent = parent.Parent {
		if parent.Namespace != "" || (foreground && (parent.Data == "picture" || pptAttr(parent, "id") == "bg")) {
			return false
		}
		for _, attr := range parent.Attr {
			if attr.Key == "hidden" {
				return false
			}
		}
		switch parent.Data {
		case "template", "noscript":
			return false
		case "body":
			return true
		}
	}
	return false
}

func (p PPTPageImages) BindHTML(db *gorm.DB, source string) (string, error) {
	if len(source) > maxPPTHTMLBytes {
		return "", fmt.Errorf("page %d requires an HTML document within 16 MiB", p.page)
	}
	htmlLower := strings.ToLower(strings.TrimSpace(source))
	if !strings.HasSuffix(htmlLower, "</html>") ||
		!(strings.HasPrefix(htmlLower, "<!doctype html") || strings.HasPrefix(htmlLower, "<html")) {
		return "", fmt.Errorf("page %d requires a complete HTML document within 16 MiB", p.page)
	}
	if len(p.materials) == 0 && p.background == "" && !strings.Contains(htmlLower, pptImageAttr) {
		return source, nil
	}
	document, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return "", fmt.Errorf("page %d: invalid HTML", p.page)
	}
	images := make(map[string][]*html.Node, len(p.materials))
	for _, id := range p.materials {
		images[id] = nil
	}
	var background *html.Node
	placements := 0
	for node := range document.Descendants() {
		if node.Type != html.ElementNode {
			continue
		}
		if p.background != "" && pptAttr(node, "id") == "bg" && pptBodyElement(node, false) {
			if background != nil {
				return "", fmt.Errorf("page %d requires a unique #bg layer", p.page)
			}
			background = node
		}
		id := pptAttr(node, pptImageAttr)
		if id == "" {
			continue
		}
		if _, approved := images[id]; !approved || node.Data != "img" || !pptBodyElement(node, true) || pptAttr(node, "srcset") != "" {
			return "", fmt.Errorf("page %d: %s must identify a bound foreground <img>", p.page, pptImageAttr)
		}
		placements++
		if placements > 24 {
			return "", fmt.Errorf("page %d has too many bound foreground placements", p.page)
		}
		images[id] = append(images[id], node)
	}
	for _, id := range p.materials {
		if len(images[id]) == 0 {
			return "", fmt.Errorf("page %d is missing <img %s=%q> for an approved material image", p.page, pptImageAttr, id)
		}
	}
	if p.background != "" && background == nil {
		return "", fmt.Errorf("page %d is missing the #bg layer for its AI background", p.page)
	}
	remaining := maxPPTHTMLBytes - len(source)
	for _, id := range p.materials {
		uri, err := pptImageURI(db, p.sessionID, "material_images", id)
		if err != nil {
			return "", fmt.Errorf("page %d: approved material %s is unavailable; verify or replace its saved file", p.page, id)
		}
		for _, node := range images[id] {
			remaining -= len(uri) - len(pptAttr(node, "src"))
			if remaining < 0 {
				return "", fmt.Errorf("page %d exceeds 16 MiB after embedding material images; use smaller images", p.page)
			}
			setPPTAttr(node, "src", uri)
		}
	}
	if background != nil {
		uri, err := pptImageURI(db, p.sessionID, "background_images", p.background)
		if err != nil {
			return "", fmt.Errorf("page %d: approved AI background is unavailable; verify or regenerate its saved file", p.page)
		}
		const marker = "/* lazymind AI background */"
		oldStyle := pptAttr(background, "style")
		style, _, _ := strings.Cut(oldStyle, marker)
		style = strings.TrimSpace(style)
		if style != "" && !strings.HasSuffix(style, ";") {
			style += ";"
		}
		style += marker + "background-image:url('" + uri + "') !important;background-size:cover !important;background-position:center !important;"
		if len(style)-len(oldStyle) > remaining {
			return "", fmt.Errorf("page %d exceeds 16 MiB after embedding its background; use a smaller image", p.page)
		}
		setPPTAttr(background, "style", style)
	}
	var result strings.Builder
	if err := html.Render(&result, document); err != nil || result.Len() > maxPPTHTMLBytes {
		return "", fmt.Errorf("page %d could not be serialized as HTML within 16 MiB", p.page)
	}
	return result.String(), nil
}

func pptOutputOrder(db *gorm.DB, session, slot string) ([]int, error) {
	var order orm.WorkflowSlotOrder
	if err := db.Where("session_id = ? AND slot_id = ?", session, slot).Find(&order).Error; err != nil {
		return nil, err
	}
	var indices []int
	if len(order.OrderList) > 0 {
		if err := json.Unmarshal(order.OrderList, &indices); err != nil {
			return nil, err
		}
	}
	return indices, nil
}

func preparePPTOutput(db *gorm.DB, attempt AttemptContext, slot string, value json.RawMessage, index *int) (json.RawMessage, error) {
	if slot != "slide_outline" && slot != "preview_html" {
		return value, nil
	}
	format, err := pptOption(db, attempt, "presentation_output_format")
	if err != nil || format != "html" {
		return value, err
	}
	source, err := pptText(value)
	if err != nil || source == "" || index == nil {
		return nil, controlstore.Reject("INVALID_ARTIFACT", slot+" requires an ordered text document; use output.value or output.value_file")
	}
	if slot == "slide_outline" {
		materials, err := pptInputs(attempt, "material_images")
		if err != nil {
			return nil, err
		}
		if _, err := pptMaterialIDs(source, materials); err != nil {
			return nil, controlstore.Reject("INVALID_ARTIFACT", err.Error())
		}
		return value, nil
	}
	order, err := pptOutputOrder(db, attempt.SessionID, slot)
	if err != nil {
		return nil, err
	}
	page := len(order) + 1
	for position, id := range order {
		if id == *index {
			page = position + 1
			break
		}
	}
	if selected := attempt.PartialSelector[slot]; len(selected) > 0 && page > len(order) {
		return nil, controlstore.Reject("INVALID_ARTIFACT", "targeted PPT publication requires an existing preview page identity")
	}
	binding, err := LoadPPTPageImages(db, attempt, page)
	if err != nil {
		return nil, controlstore.Reject("INVALID_ARTIFACT", err.Error())
	}
	ready, err := binding.BindHTML(db, source)
	if err != nil {
		return nil, controlstore.Reject("INVALID_ARTIFACT", err.Error())
	}
	// Keep existing list identity and other text artifact metadata.
	var body map[string]json.RawMessage
	if err := json.Unmarshal(common.CanonicalizeTextArtifactValue("text", value), &body); err != nil || body == nil || body["storage"] != nil {
		return nil, controlstore.Reject("INVALID_ARTIFACT", "preview_html requires a text artifact")
	}
	delete(body, "data")
	body["text"], _ = json.Marshal(ready)
	return json.Marshal(body)
}

func validatePPTCompletion(db *gorm.DB, attempt AttemptContext, rows []orm.WorkflowSlotRevision) error {
	switch attempt.StepID {
	case "plan_page_prompts":
		return validatePPTMaterialPlan(db, attempt, rows)
	case "generate_backgrounds":
		return validatePPTPageCount(attempt, rows, "background_images", "background_prompts")
	case "generate_ppt":
		format, err := pptOption(db, attempt, "presentation_output_format")
		if err != nil || format != "html" {
			return err
		}
		return validatePPTPageCount(attempt, rows, "preview_html", "slide_outline")
	default:
		return nil
	}
}

func validatePPTPageCount(attempt AttemptContext, rows []orm.WorkflowSlotRevision, slot, inputSlot string) error {
	saved := map[int]bool{}
	for _, row := range rows {
		if row.SlotID == slot && row.ListIndex != nil {
			saved[*row.ListIndex] = true
		}
	}
	if selected := attempt.PartialSelector[slot]; len(selected) > 0 {
		for _, index := range selected {
			if !saved[index] {
				return fmt.Errorf("%s has no saved output for requested list index %d", slot, index)
			}
		}
		return nil
	}
	inputs, err := pptInputs(attempt, inputSlot)
	if err != nil {
		return err
	}
	if len(inputs) == 0 || len(saved) != len(inputs) {
		return fmt.Errorf("%s has %d saved pages; expected %d from %s", slot, len(saved), len(inputs), inputSlot)
	}
	return nil
}

// Collection publishes only images selected for the deck. A full plan must
// assign each of those images, while individual pages may be text-only.
func validatePPTMaterialPlan(db *gorm.DB, attempt AttemptContext, rows []orm.WorkflowSlotRevision) error {
	format, err := pptOption(db, attempt, "presentation_output_format")
	if err != nil || format != "html" || len(attempt.PartialSelector["slide_outline"]) > 0 {
		return err
	}
	materials, err := pptInputs(attempt, "material_images")
	if err != nil || len(materials) == 0 {
		return err
	}
	used := map[string]bool{}
	for _, row := range rows {
		if row.SlotID != "slide_outline" {
			continue
		}
		value, err := controlstore.ResolveValue(db, row)
		if err != nil {
			return err
		}
		brief, err := pptText(value)
		if err != nil {
			return err
		}
		ids, err := pptMaterialIDs(brief, materials)
		if err != nil {
			return err
		}
		for _, id := range ids {
			used[id] = true
		}
	}
	for _, image := range materials {
		if !used[image.RevisionID] {
			return fmt.Errorf("selected material image %s has no binding in any page prompt", image.RevisionID)
		}
	}
	return nil
}
