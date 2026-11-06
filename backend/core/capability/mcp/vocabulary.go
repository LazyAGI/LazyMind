package mcpadapter

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lazymind/core/capability"
)

func ChatVocabularyTools() []string {
	return []string{"vocabulary.wordbook.list", "vocabulary.word.list"}
}

func newVocabularyServer(service *capability.Service) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "lazymind-vocabulary", Version: "v1"}, nil)
	addVocabularyTools(server, service)
	return server
}

func addVocabularyTools(server *mcp.Server, service *capability.Service) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "vocabulary.wordbook.list", Title: "List vocabulary wordbooks",
		Description: "List wordbooks from the user's currently selected vocabulary backend.", Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, request *mcp.CallToolRequest, input capability.ListVocabularyWordbooksInput) (*mcp.CallToolResult, capability.ListVocabularyWordbooksResult, error) {
		result, err := service.ListVocabularyWordbooks(ctx, invocation(ctx, request), input)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "vocabulary.word.list", Title: "List vocabulary words",
		Description: "List words in a selected wordbook or deck. Set due_only=true whenever the user asks which words need review; this uses the authoritative scheduler and must not be inferred from state or review_count.", Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, request *mcp.CallToolRequest, input capability.ListVocabularyWordsInput) (*mcp.CallToolResult, capability.ListVocabularyWordsResult, error) {
		result, err := service.ListVocabularyWords(ctx, invocation(ctx, request), input)
		return nil, result, err
	})
}
