package source

import "time"

type CreateOperation struct {
	OperationID                  string
	CallerID                     string
	RequestID                    string
	RequestHash                  string
	SourceID                     string
	DatasetID                    string
	CreatedCoreParentDocumentIDs JSON
	CreatedBindingIDs            JSON
	Warning                      JSON
	Status                       string
	CompensationStatus           string
	CompensationError            JSON
	CreatedAt                    time.Time
	UpdatedAt                    time.Time
}
