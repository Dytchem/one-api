package render

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
)

func StringData(c *gin.Context, str string) {
	str = strings.TrimPrefix(str, "data: ")
	str = strings.TrimSuffix(str, "\r")
	c.Render(-1, common.CustomEvent{Data: "data: " + str})
	c.Writer.Flush()
}

// StringEventData 输出带 event: 类型的 SSE 行（dyt-53，Responses 流式）
func StringEventData(c *gin.Context, eventType string, str string) {
	str = strings.TrimPrefix(str, "data: ")
	str = strings.TrimSuffix(str, "\r")
	c.Render(-1, common.CustomEvent{Event: eventType, Data: "data: " + str})
	c.Writer.Flush()
}

func ObjectData(c *gin.Context, object interface{}) error {
	jsonData, err := json.Marshal(object)
	if err != nil {
		return fmt.Errorf("error marshalling object: %w", err)
	}
	StringData(c, string(jsonData))
	return nil
}

func Done(c *gin.Context) {
	StringData(c, "[DONE]")
}
