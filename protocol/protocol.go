package protocol

type Action int

const (
	ActionVersion = iota
	ActionError
	ActionList
	ActionFetch
)

type Request struct {
	Action      Action
	Error       string
	SrcID       SrcID
	Cached      map[SrcID][]byte
	GOARCH      string
	GOOS        string
	BuildTags   []string
	ReleaseTags []string
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
