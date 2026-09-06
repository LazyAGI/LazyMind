package feishu

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

const feishuCLIIdentityArgument = "--as user"

type FeishuCLIClient struct {
	coreBaseURL   *url.URL
	internalToken string
	httpClient    *http.Client
}

type routingFeishuClient struct {
	httpClient FeishuClient
	cliClient  FeishuClient
}

type feishuCLIExecuteResponse struct {
	Data          json.RawMessage `json:"data"`
	ContentBase64 string          `json:"content_base64"`
}

func NewFeishuCLIClient(coreBaseURL, internalToken string, client *http.Client) (*FeishuCLIClient, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(coreBaseURL), "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.TrimSpace(internalToken) == "" {
		return nil, errors.New("Feishu CLI client configuration is invalid")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("Feishu CLI client configuration is invalid")
	}
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	return &FeishuCLIClient{coreBaseURL: parsed, internalToken: strings.TrimSpace(internalToken), httpClient: client}, nil
}

func NewRoutingFeishuClient(httpClient, cliClient FeishuClient) (FeishuClient, error) {
	if httpClient == nil || cliClient == nil {
		return nil, errors.New("Feishu clients are required")
	}
	return &routingFeishuClient{httpClient: httpClient, cliClient: cliClient}, nil
}

func (client *routingFeishuClient) route(credential string) FeishuClient {
	if strings.HasPrefix(credential, "lmc_fcli_") {
		return client.cliClient
	}
	return client.httpClient
}

func (client *routingFeishuClient) GetDriveRoot(ctx context.Context, token string) (Object, error) {
	return client.route(token).GetDriveRoot(ctx, token)
}

func (client *routingFeishuClient) GetDriveFolder(ctx context.Context, token, folderToken string) (Object, error) {
	return client.route(token).GetDriveFolder(ctx, token, folderToken)
}

func (client *routingFeishuClient) ListDriveChildren(ctx context.Context, token, folderToken, cursor string, pageSize int) (ObjectPage, error) {
	return client.route(token).ListDriveChildren(ctx, token, folderToken, cursor, pageSize)
}

func (client *routingFeishuClient) DownloadDriveFile(ctx context.Context, token, fileToken, expectedVersion string) (ExportedContent, error) {
	return client.route(token).DownloadDriveFile(ctx, token, fileToken, expectedVersion)
}

func (client *routingFeishuClient) ExportDriveDocumentMarkdown(ctx context.Context, token, docToken, expectedVersion string) (ExportedContent, error) {
	return client.route(token).ExportDriveDocumentMarkdown(ctx, token, docToken, expectedVersion)
}

func (client *routingFeishuClient) ListWikiSpaces(ctx context.Context, token, cursor string, pageSize int) (ObjectPage, error) {
	return client.route(token).ListWikiSpaces(ctx, token, cursor, pageSize)
}

func (client *routingFeishuClient) GetWikiNode(ctx context.Context, token, spaceID, nodeToken string) (Object, error) {
	return client.route(token).GetWikiNode(ctx, token, spaceID, nodeToken)
}

func (client *routingFeishuClient) ListWikiChildren(ctx context.Context, token, spaceID, nodeToken, cursor string, pageSize int) (ObjectPage, error) {
	return client.route(token).ListWikiChildren(ctx, token, spaceID, nodeToken, cursor, pageSize)
}

func (client *routingFeishuClient) ExportWikiNodeMarkdown(ctx context.Context, token, spaceID, nodeToken, expectedVersion string) (ExportedContent, error) {
	return client.route(token).ExportWikiNodeMarkdown(ctx, token, spaceID, nodeToken, expectedVersion)
}

func (client *FeishuCLIClient) GetDriveRoot(context.Context, string) (Object, error) {
	return Object{
		Kind: ObjectKindDriveFolder, Token: "root", Name: "Drive Root",
		IsContainer: true, HasChildren: true, Revision: "root", StableID: "root",
	}, nil
}

func (client *FeishuCLIClient) GetDriveFolder(ctx context.Context, handle, folderToken string) (Object, error) {
	folderToken = strings.TrimSpace(folderToken)
	if folderToken == "" || folderToken == "root" {
		return client.GetDriveRoot(ctx, handle)
	}
	if _, err := client.ListDriveChildren(ctx, handle, folderToken, "", 1); err != nil {
		return Object{}, err
	}
	return driveFolderObject(map[string]any{"token": folderToken, "name": folderToken}, folderToken), nil
}

func (client *FeishuCLIClient) ListDriveChildren(ctx context.Context, handle, folderToken, cursor string, pageSize int) (ObjectPage, error) {
	response, err := client.execute(ctx, handle, "drive_list", map[string]string{
		"folder_token": folderToken, "cursor": cursor, "page_size": strconv.Itoa(pageSize),
	})
	if err != nil {
		return ObjectPage{}, err
	}
	var data openAPIDriveFiles
	if err := decodeCLIData(response.Data, &data); err != nil {
		return ObjectPage{}, err
	}
	return driveObjectPage(data, folderToken), nil
}

func (client *FeishuCLIClient) DownloadDriveFile(ctx context.Context, handle, fileToken, expectedVersion string) (ExportedContent, error) {
	response, err := client.execute(ctx, handle, "file_download", map[string]string{"file_token": fileToken})
	if err != nil {
		return ExportedContent{}, err
	}
	content, err := base64.StdEncoding.DecodeString(response.ContentBase64)
	if err != nil || len(content) == 0 {
		return ExportedContent{}, connector.NewError(connector.ErrorCodeTransient, "decode Feishu CLI download failed")
	}
	return ExportedContent{Content: content, SizeBytes: int64(len(content)), ExportedVersion: expectedVersion}, nil
}

func (client *FeishuCLIClient) ExportDriveDocumentMarkdown(ctx context.Context, handle, docToken, expectedVersion string) (ExportedContent, error) {
	content, err := client.rawContent(ctx, handle, "docx", docToken)
	if err != nil {
		content, err = client.rawContent(ctx, handle, "doc", docToken)
		if err != nil {
			return ExportedContent{}, err
		}
	}
	return ExportedContent{Content: []byte(content), MimeType: "text/markdown", FileExtension: ".md", SizeBytes: int64(len(content)), ExportedVersion: expectedVersion}, nil
}

func (client *FeishuCLIClient) ListWikiSpaces(ctx context.Context, handle, cursor string, pageSize int) (ObjectPage, error) {
	response, err := client.execute(ctx, handle, "wiki_spaces", map[string]string{"cursor": cursor, "page_size": strconv.Itoa(pageSize)})
	if err != nil {
		return ObjectPage{}, err
	}
	var data openAPIWikiSpaces
	if err := decodeCLIData(response.Data, &data); err != nil {
		return ObjectPage{}, err
	}
	return wikiSpacesPage(data), nil
}

func (client *FeishuCLIClient) GetWikiNode(ctx context.Context, handle, spaceID, nodeToken string) (Object, error) {
	response, err := client.execute(ctx, handle, "wiki_node", map[string]string{"node_token": nodeToken})
	if err != nil {
		return Object{}, err
	}
	var data map[string]any
	if err := decodeCLIData(response.Data, &data); err != nil {
		return Object{}, err
	}
	return wikiNodeObject(openAPIMapValue(data["node"], data), spaceID, nodeToken), nil
}

func (client *FeishuCLIClient) ListWikiChildren(ctx context.Context, handle, spaceID, nodeToken, cursor string, pageSize int) (ObjectPage, error) {
	response, err := client.execute(ctx, handle, "wiki_children", map[string]string{
		"space_id": spaceID, "node_token": nodeToken, "cursor": cursor, "page_size": strconv.Itoa(pageSize),
	})
	if err != nil {
		return ObjectPage{}, err
	}
	var data openAPIWikiNodes
	if err := decodeCLIData(response.Data, &data); err != nil {
		return ObjectPage{}, err
	}
	return wikiNodesPage(data, spaceID, nodeToken), nil
}

func (client *FeishuCLIClient) ExportWikiNodeMarkdown(ctx context.Context, handle, spaceID, nodeToken, expectedVersion string) (ExportedContent, error) {
	node, err := client.GetWikiNode(ctx, handle, spaceID, nodeToken)
	if err != nil {
		return ExportedContent{}, err
	}
	objectType := normalizedFeishuObjectType(firstNonEmpty(node.DriveType, node.FileExtension))
	objectToken := firstNonEmpty(node.StableID, node.Token)
	if !isFeishuDocType(objectType) && objectType != "" && objectType != "md" {
		return client.DownloadDriveFile(ctx, handle, objectToken, expectedVersion)
	}
	if objectType == "" || objectType == "md" {
		objectType = "docx"
	}
	content, err := client.rawContent(ctx, handle, objectType, objectToken)
	if err != nil {
		return ExportedContent{}, err
	}
	return ExportedContent{Content: []byte(content), MimeType: "text/markdown", FileExtension: ".md", SizeBytes: int64(len(content)), ExportedVersion: expectedVersion}, nil
}

func (client *FeishuCLIClient) rawContent(ctx context.Context, handle, objectType, objectToken string) (string, error) {
	operation := "docx_raw"
	if objectType == "doc" {
		operation = "doc_raw"
	}
	response, err := client.execute(ctx, handle, operation, map[string]string{"document_token": objectToken})
	if err != nil {
		return "", err
	}
	var data struct {
		Content string `json:"content"`
	}
	if err := decodeCLIData(response.Data, &data); err != nil || data.Content == "" {
		return "", connector.NewError(connector.ErrorCodeTransient, "decode Feishu CLI document failed")
	}
	return data.Content, nil
}

func (client *FeishuCLIClient) execute(ctx context.Context, handle, operation string, params map[string]string) (feishuCLIExecuteResponse, error) {
	if client == nil || !strings.HasPrefix(handle, "lmc_fcli_") {
		return feishuCLIExecuteResponse{}, connector.NewError(ErrorCodeAuthInvalid, "Feishu CLI authorization is unavailable")
	}
	body, err := json.Marshal(map[string]any{
		"handle": handle, "operation": operation, "params": params,
		"identity": strings.TrimPrefix(feishuCLIIdentityArgument, "--as "),
	})
	if err != nil {
		return feishuCLIExecuteResponse{}, connector.NewError(connector.ErrorCodeInvalidArgument, "encode Feishu CLI request failed")
	}
	endpoint := *client.coreBaseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/v1/internal/provider-connections/feishu-cli:execute"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return feishuCLIExecuteResponse{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-LazyMind-Internal-Token", client.internalToken)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return feishuCLIExecuteResponse{}, connector.NewError(connector.ErrorCodeTransient, "Feishu CLI service is unavailable")
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 140<<20))
	if err != nil || response.StatusCode != http.StatusOK {
		return feishuCLIExecuteResponse{}, connector.NewError(connector.ErrorCodeTransient, "Feishu CLI request failed")
	}
	var output feishuCLIExecuteResponse
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&output); err != nil {
		return feishuCLIExecuteResponse{}, connector.NewError(connector.ErrorCodeTransient, "decode Feishu CLI response failed")
	}
	return output, nil
}

func decodeCLIData(payload json.RawMessage, output any) error {
	if len(payload) == 0 || output == nil {
		return connector.NewError(connector.ErrorCodeTransient, "Feishu CLI response is empty")
	}
	if err := json.Unmarshal(payload, output); err != nil {
		return connector.NewError(connector.ErrorCodeTransient, "decode Feishu CLI data failed")
	}
	return nil
}
