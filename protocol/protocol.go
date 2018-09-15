package protocol

type ErrorRequest struct {
	Error string `json:"error"`
}

type FetchRequest struct {
	SrcID  SrcID
	Cached []FileID
}

type FetchResponse struct {
	Files    map[string]FileID
	Contents map[FileID][]byte
	Error    string
}

type SrcID struct {
	ImportPath   string
	IncludeTests bool
}

type FileID string

type ChangedEvent struct{}

func (t ChangedEvent) Data() string  { return "changed" }
func (t ChangedEvent) Event() string { return "" }
func (t ChangedEvent) Id() string    { return "" }
