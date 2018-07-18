package protocol

type ErrorRequest struct {
	Error string `json:"error"`
}

type FetchRequest struct {
	SrcID       SrcID
	Cached      map[SrcID][]byte
	GOARCH      string
	GOOS        string
	BuildTags   []string
	ReleaseTags []string
}

type FetchResponse struct {
	Srcs  Srcs
	Error string
}

type SrcID struct {
	ImportPath   string
	IncludeTests bool
}

type Src struct {
	Files map[string]string
	Hash  []byte
}

type Srcs map[SrcID]*Src
