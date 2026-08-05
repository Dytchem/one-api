package model

type Tool struct {
	Id       string   `json:"id,omitempty"`
	Index    int      `json:"index,omitempty"` // 流式 tool_calls 增量按 index 归并（dyt-53）
	Type     string   `json:"type,omitempty"`  // when splicing claude tools stream messages, it is empty
	Function Function `json:"function"`
}

type Function struct {
	Description string `json:"description,omitempty"`
	Name        string `json:"name,omitempty"`       // when splicing claude tools stream messages, it is empty
	Parameters  any    `json:"parameters,omitempty"` // request
	Arguments   any    `json:"arguments,omitempty"`  // response
}
