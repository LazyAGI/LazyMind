package externalagent

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const maxExternalRequestViewBytes = 12 << 10

func projectExternalRequest(
	requestID, kind, summary string,
	payload json.RawMessage,
) (ExternalRequest, map[string]json.RawMessage) {
	view := ExternalRequest{
		RequestID: boundedRunes(requestID, 512),
		Kind:      boundedRunes(kind, 100),
		Summary:   boundedRunes(summary, 1000),
	}
	responses := make(map[string]json.RawMessage)
	switch kind {
	case "command_approval":
		projectCommandRequest(&view, responses, payload)
	case "file_change_approval":
		projectFileChangeRequest(&view, responses, payload)
	case "permissions_approval":
		projectPermissionsRequest(&view, responses, payload)
	case "user_input":
		projectUserInputRequest(&view, responses, payload)
	default:
		view.Error = "Codex returned an unsupported interactive request."
	}
	encoded, err := json.Marshal(view)
	if err == nil && len(encoded) <= maxExternalRequestViewBytes {
		return view, responses
	}
	var reject *ExternalRequestAction
	for index := range view.Actions {
		if view.Actions[index].Kind == "deny" {
			value := view.Actions[index]
			reject = &value
			break
		}
	}
	view.Fields = nil
	view.Questions = nil
	view.Actions = nil
	view.Error = "Codex request exceeds the safe interactive-view limit."
	boundedResponses := make(map[string]json.RawMessage)
	if reject != nil {
		view.Actions = []ExternalRequestAction{*reject}
		boundedResponses[reject.ID] = responses[reject.ID]
	}
	return view, boundedResponses
}

func projectCommandRequest(
	view *ExternalRequest,
	responses map[string]json.RawMessage,
	payload json.RawMessage,
) {
	var body map[string]json.RawMessage
	if json.Unmarshal(payload, &body) != nil || body == nil {
		view.Error = "Codex command approval payload is invalid."
		return
	}
	command, commandProvided, commandValid := boundedOptionalString(body, "command", 2000)
	cwd, _, cwdValid := boundedOptionalString(body, "cwd", 500)
	reason, _, reasonValid := boundedOptionalString(body, "reason", 1000)
	additionalPermissions, additionalProvided, additionalValid := boundedOptionalJSON(
		body, "additionalPermissions", 2000,
	)
	policy := map[string]json.RawMessage{}
	for _, key := range []string{
		"networkApprovalContext",
		"environmentId",
		"itemId",
		"approvalId",
		"proposedExecpolicyAmendment",
		"proposedNetworkPolicyAmendments",
	} {
		if raw, ok := body[key]; ok && len(raw) > 0 && string(raw) != "null" {
			policy[key] = raw
		}
	}
	_, networkProvided := policy["networkApprovalContext"]
	networkValid := true
	if networkProvided {
		var network map[string]any
		raw := policy["networkApprovalContext"]
		networkValid = json.Unmarshal(raw, &network) == nil && len(network) > 0
	}
	if (strings.TrimSpace(command) == "" && !networkProvided) ||
		(commandProvided && !commandValid) ||
		!networkValid || !cwdValid || !reasonValid || !additionalValid {
		view.Error = "Codex command approval is incomplete or invalid."
		projectCommandDeny(view, responses, payload)
		return
	}
	appendField(view, "command", command, "")
	appendField(view, "cwd", cwd, "")
	appendField(view, "reason", reason, "")
	if additionalProvided {
		appendField(view, "additional_permissions", additionalPermissions, "json")
	}
	if len(policy) > 0 {
		encoded, err := json.Marshal(policy)
		if err != nil || len(encoded) > 3000 {
			view.Error = "Codex command policy context is too large."
			view.Fields = nil
			projectCommandDeny(view, responses, payload)
			return
		} else {
			view.Fields = append(view.Fields, ExternalRequestField{
				Kind: "policy", Value: string(encoded), Language: "json",
			})
		}
	}
	available, err := commandAvailableDecisions(payload)
	if err != nil {
		view.Error = err.Error()
		return
	}
	policyIndex := 0
	for _, decision := range available {
		kind, label := commandDecisionView(decision)
		if kind == "" {
			view.Error = "Codex command approval contains an unsupported decision."
			view.Actions = nil
			clear(responses)
			return
		}
		if kind == "policy" {
			policyIndex++
			label = boundedRunes(
				fmt.Sprintf("策略 %d · %s", policyIndex, label),
				200,
			)
			encoded, marshalErr := json.Marshal(decision)
			if marshalErr != nil || len(encoded) > 3000 {
				view.Error = "Codex command policy amendment is too large."
				view.Actions = nil
				clear(responses)
				projectCommandDeny(view, responses, payload)
				return
			}
			appendField(
				view,
				label,
				string(encoded),
				"json",
			)
		}
		addRequestAction(view, responses, kind, label, map[string]any{
			"decision": decision,
		})
	}
}

func projectCommandDeny(
	view *ExternalRequest,
	responses map[string]json.RawMessage,
	payload json.RawMessage,
) {
	view.Actions = nil
	clear(responses)
	available, err := commandAvailableDecisions(payload)
	if err != nil {
		return
	}
	for _, decision := range available {
		text, ok := decision.(string)
		if ok && (text == "decline" || text == "cancel") {
			addRequestAction(view, responses, "deny", "", map[string]any{
				"decision": text,
			})
			return
		}
	}
}

func commandDecisionView(decision any) (string, string) {
	if text, ok := decision.(string); ok {
		switch text {
		case "accept":
			return "allow_once", ""
		case "acceptForSession":
			return "allow_session", ""
		case "decline", "cancel":
			return "deny", ""
		}
		return "", ""
	}
	value, ok := decision.(map[string]any)
	if !ok || validateCommandDecision(decision) != nil {
		return "", ""
	}
	if raw, ok := value["applyNetworkPolicyAmendment"].(map[string]any); ok {
		amendment, _ := raw["network_policy_amendment"].(map[string]any)
		host, _ := amendment["host"].(string)
		action, _ := amendment["action"].(string)
		return "policy", boundedRunes(action+" "+host, 200)
	}
	raw, _ := value["acceptWithExecpolicyAmendment"].(map[string]any)
	parts, _ := raw["execpolicy_amendment"].([]any)
	labels := make([]string, 0, len(parts))
	for _, part := range parts {
		labels = append(labels, fmt.Sprint(part))
	}
	return "policy", boundedRunes(strings.Join(labels, " && "), 160)
}

func projectFileChangeRequest(
	view *ExternalRequest,
	responses map[string]json.RawMessage,
	payload json.RawMessage,
) {
	var body struct {
		GrantRoot        string `json:"grantRoot"`
		Reason           string `json:"reason"`
		ItemID           string `json:"itemId"`
		ChangesTruncated bool   `json:"changesTruncated"`
		Changes          []struct {
			Path string `json:"path"`
			Kind struct {
				Type     string `json:"type"`
				MovePath string `json:"move_path"`
			} `json:"kind"`
			Diff string `json:"diff"`
		} `json:"changes"`
	}
	valid := json.Unmarshal(payload, &body) == nil &&
		!body.ChangesTruncated && len(body.Changes) > 0 && len(body.Changes) <= 10 &&
		utf8.RuneCountInString(body.GrantRoot) <= 500 &&
		utf8.RuneCountInString(body.Reason) <= 1000 &&
		body.ItemID != "" && utf8.RuneCountInString(body.ItemID) <= 512
	if valid {
		appendField(view, "file_scope", body.GrantRoot, "")
		appendField(view, "item_id", body.ItemID, "")
		appendField(view, "reason", body.Reason, "")
		for _, change := range body.Changes {
			if change.Path == "" || utf8.RuneCountInString(change.Path) > 500 ||
				(change.Kind.Type != "add" && change.Kind.Type != "delete" && change.Kind.Type != "update") ||
				utf8.RuneCountInString(change.Kind.MovePath) > 500 ||
				change.Diff == "" || utf8.RuneCountInString(change.Diff) > 4000 {
				valid = false
				break
			}
			label := change.Kind.Type + " · " + change.Path
			if change.Kind.MovePath != "" {
				label += " → " + change.Kind.MovePath
			}
			appendField(view, "file", label, "")
			appendField(view, "diff", change.Diff, "diff")
		}
	}
	if !valid {
		view.Fields = nil
		view.Error = "Codex did not provide a complete, reviewable file change."
	} else {
		addRequestAction(view, responses, "allow_once", "", map[string]any{"decision": "accept"})
	}
	addRequestAction(view, responses, "deny", "", map[string]any{"decision": "decline"})
}

func projectPermissionsRequest(
	view *ExternalRequest,
	responses map[string]json.RawMessage,
	payload json.RawMessage,
) {
	var body map[string]json.RawMessage
	if json.Unmarshal(payload, &body) != nil || body == nil {
		view.Error = "Codex permissions payload is invalid."
		addRequestAction(view, responses, "deny", "", map[string]any{
			"permissions": map[string]any{}, "scope": "turn",
		})
		return
	}
	var permissions map[string]any
	if json.Unmarshal(body["permissions"], &permissions) != nil || permissions == nil {
		view.Error = "Codex permissions payload is invalid."
		addRequestAction(view, responses, "deny", "", map[string]any{
			"permissions": map[string]any{}, "scope": "turn",
		})
		return
	}
	encoded, err := json.Marshal(permissions)
	if err != nil || len(encoded) > 3000 {
		view.Error = "Codex permissions payload is too large."
		addRequestAction(view, responses, "deny", "", map[string]any{
			"permissions": map[string]any{}, "scope": "turn",
		})
		return
	}
	appendField(view, "permissions", string(encoded), "json")
	for _, key := range []string{"environmentId", "cwd", "reason", "itemId"} {
		appendStringField(view, key, body[key], map[string]int{
			"environmentId": 500, "cwd": 500, "reason": 1000, "itemId": 512,
		}[key], "")
	}
	addRequestAction(view, responses, "grant_turn", "", map[string]any{
		"permissions": permissions, "scope": "turn",
	})
	addRequestAction(view, responses, "grant_session", "", map[string]any{
		"permissions": permissions, "scope": "session",
	})
	addRequestAction(view, responses, "deny", "", map[string]any{
		"permissions": map[string]any{}, "scope": "turn",
	})
}

func projectUserInputRequest(
	view *ExternalRequest,
	responses map[string]json.RawMessage,
	payload json.RawMessage,
) {
	var body struct {
		Questions []struct {
			ID       string `json:"id"`
			Header   string `json:"header"`
			Question string `json:"question"`
			IsOther  bool   `json:"isOther"`
			Options  []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
		} `json:"questions"`
	}
	if json.Unmarshal(payload, &body) != nil || len(body.Questions) == 0 || len(body.Questions) > 10 {
		view.Error = "Codex user-input questions are invalid."
		addRequestAction(view, responses, "deny", "", map[string]any{"answers": map[string]any{}})
		return
	}
	seen := map[string]bool{}
	for _, question := range body.Questions {
		text := question.Question
		if text == "" {
			text = question.Header
		}
		if question.ID == "" || seen[question.ID] || text == "" ||
			utf8.RuneCountInString(question.ID) > 512 ||
			utf8.RuneCountInString(question.Header) > 500 ||
			utf8.RuneCountInString(text) > 500 || len(question.Options) > 20 {
			view.Error = "Codex user-input questions are invalid."
			view.Questions = nil
			break
		}
		seen[question.ID] = true
		projected := ExternalRequestQuestion{
			ID: boundedRunes(question.ID, 512), Header: boundedRunes(question.Header, 500),
			Question: boundedRunes(text, 500), AllowOther: question.IsOther,
		}
		optionSeen := map[string]bool{}
		for _, option := range question.Options {
			if option.Label == "" || optionSeen[option.Label] ||
				utf8.RuneCountInString(option.Label) > 80 ||
				utf8.RuneCountInString(option.Description) > 300 {
				view.Error = "Codex user-input options are invalid."
				view.Questions = nil
				break
			}
			optionSeen[option.Label] = true
			projected.Options = append(projected.Options, ExternalRequestOption{
				Label: option.Label, Description: option.Description,
			})
		}
		if view.Error != "" {
			break
		}
		view.Questions = append(view.Questions, projected)
	}
	if view.Error == "" {
		addRequestAction(view, responses, "submit", "", nil)
	}
	addRequestAction(view, responses, "deny", "", map[string]any{"answers": map[string]any{}})
}

func responseForExternalRequest(
	request *pendingRequest,
	input RequestResponse,
) (json.RawMessage, error) {
	response, ok := request.Responses[input.ActionID]
	if !ok || input.ActionID == "" {
		return nil, fmt.Errorf("invalid request: action is not available")
	}
	if response != nil {
		return append(json.RawMessage(nil), response...), nil
	}
	if request.View.Kind != "user_input" || requestActionKind(request.View, input.ActionID) != "submit" {
		return nil, fmt.Errorf("invalid request: action has no response")
	}
	if err := validateExternalRequestAnswers(request.View.Questions, input.Answers); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"answers": input.Answers})
}

func validateExternalRequestAnswers(
	questions []ExternalRequestQuestion,
	answers map[string]ExternalRequestAnswer,
) error {
	if len(questions) == 0 || len(answers) != len(questions) {
		return fmt.Errorf("invalid request: user input answers")
	}
	for _, question := range questions {
		answer, ok := answers[question.ID]
		if !ok || len(answer.Answers) == 0 || len(answer.Answers) > 20 {
			return fmt.Errorf("invalid request: user input answer values")
		}
		for _, value := range answer.Answers {
			if value == "" || utf8.RuneCountInString(value) > 1000 {
				return fmt.Errorf("invalid request: user input answer value")
			}
		}
		if len(question.Options) > 0 {
			if len(answer.Answers) != 1 {
				return fmt.Errorf("invalid request: user input choice count")
			}
			matched := false
			for _, option := range question.Options {
				if answer.Answers[0] == option.Label {
					matched = true
					break
				}
			}
			if !matched && !question.AllowOther {
				return fmt.Errorf("invalid request: user input choice")
			}
		}
	}
	return nil
}

func requestActionKind(view ExternalRequest, actionID string) string {
	for _, action := range view.Actions {
		if action.ID == actionID {
			return action.Kind
		}
	}
	return ""
}

func addRequestAction(
	view *ExternalRequest,
	responses map[string]json.RawMessage,
	kind, label string,
	response any,
) {
	actionID := fmt.Sprintf("a%d", len(view.Actions)+1)
	view.Actions = append(view.Actions, ExternalRequestAction{
		ID: actionID, Kind: kind, Label: boundedRunes(label, 200),
	})
	if response == nil {
		responses[actionID] = nil
		return
	}
	encoded, err := json.Marshal(response)
	if err == nil {
		responses[actionID] = encoded
	}
}

func appendStringField(
	view *ExternalRequest,
	kind string,
	raw json.RawMessage,
	limit int,
	language string,
) {
	var value string
	if json.Unmarshal(raw, &value) == nil && utf8.RuneCountInString(value) <= limit {
		appendField(view, kind, value, language)
	}
}

func appendJSONField(
	view *ExternalRequest,
	kind string,
	raw json.RawMessage,
	limit int,
) {
	if len(raw) == 0 || string(raw) == "null" {
		return
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return
	}
	encoded, err := json.Marshal(value)
	if err == nil && len(encoded) <= limit {
		appendField(view, kind, string(encoded), "json")
	}
}

func boundedOptionalString(
	body map[string]json.RawMessage,
	key string,
	limit int,
) (string, bool, bool) {
	raw, provided := body[key]
	if !provided || len(raw) == 0 || string(raw) == "null" {
		return "", false, true
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || utf8.RuneCountInString(value) > limit {
		return "", true, false
	}
	return value, true, true
}

func boundedOptionalJSON(
	body map[string]json.RawMessage,
	key string,
	limit int,
) (string, bool, bool) {
	raw, provided := body[key]
	if !provided || len(raw) == 0 || string(raw) == "null" {
		return "", false, true
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return "", true, false
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > limit {
		return "", true, false
	}
	return string(encoded), true, true
}

func appendField(view *ExternalRequest, kind, value, language string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	view.Fields = append(view.Fields, ExternalRequestField{
		Kind: boundedRunes(kind, 100), Value: value, Language: boundedRunes(language, 20),
	})
}

func boundedRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
