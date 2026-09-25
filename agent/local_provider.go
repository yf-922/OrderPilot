package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type LocalProvider struct{}

func NewLocalProvider() *LocalProvider { return &LocalProvider{} }
func (p *LocalProvider) Name() string  { return "local" }
func (p *LocalProvider) Model() string { return "deterministic-demo" }

var orderIDPattern = regexp.MustCompile(`\d+`)

func (p *LocalProvider) Complete(_ context.Context, input CompletionRequest) (Completion, error) {
	if len(input.Messages) == 0 {
		return Completion{}, fmt.Errorf("messages are required")
	}
	last := input.Messages[len(input.Messages)-1]
	if last.Role == "tool" {
		return Completion{Message: Message{Role: "assistant", Content: summarizeLocalTool(last.Name, last.Content)}}, nil
	}
	text := strings.TrimSpace(last.Content)
	lower := strings.ToLower(text)
	call := ToolCall{ID: "local-call-1"}
	switch {
	case (strings.Contains(text, "取消") || strings.Contains(lower, "cancel")) && firstOrderID(text) > 0:
		call.Name = "prepare_cancel_order"
		call.Arguments = mustJSON(map[string]any{"order_id": firstOrderID(text)})
	case (strings.Contains(text, "订单") || strings.Contains(lower, "order")) && firstOrderID(text) > 0:
		call.Name = "get_order_status"
		call.Arguments = mustJSON(map[string]any{"order_id": firstOrderID(text)})
	case strings.Contains(text, "订单") || strings.Contains(text, "购买") || strings.Contains(lower, "order"):
		call.Name = "list_my_orders"
		call.Arguments = mustJSON(map[string]any{"page": 1, "page_size": 10})
	case strings.Contains(text, "库存") || strings.Contains(text, "商品") || strings.Contains(lower, "product") || strings.Contains(lower, "stock"):
		call.Name = "search_products"
		call.Arguments = mustJSON(map[string]any{"keyword": "", "low_stock_only": strings.Contains(text, "不足") || strings.Contains(text, "低库存") || strings.Contains(text, "少于") || strings.Contains(lower, "low stock")})
	default:
		return Completion{Message: Message{Role: "assistant", Content: "我可以查询商品库存、查看你的订单状态，并在你确认后取消待处理订单。你可以问：我的最近订单有哪些？"}}, nil
	}
	return Completion{Message: Message{Role: "assistant", ToolCalls: []ToolCall{call}}}, nil
}

func firstOrderID(text string) int64 {
	value := orderIDPattern.FindString(text)
	id, _ := strconv.ParseInt(value, 10, 64)
	return id
}

func mustJSON(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

func summarizeLocalTool(name, content string) string {
	var result map[string]any
	if json.Unmarshal([]byte(content), &result) != nil {
		return "工具返回了无法解析的结果，请稍后重试。"
	}
	if ok, _ := result["ok"].(bool); !ok {
		return toolFailureMessage(result["error"])
	}
	switch name {
	case "search_products":
		items, _ := result["items"].([]any)
		return fmt.Sprintf("已查询到 %d 个符合条件的商品，详细库存见右侧工具调用结果。", len(items))
	case "list_my_orders":
		return fmt.Sprintf("已查询你的订单，共 %v 条，详细结果见右侧。", result["total"])
	case "get_order_status":
		return fmt.Sprintf("订单 %v 当前状态是“%v”。", result["orderId"], result["statusText"])
	case "prepare_cancel_order":
		return fmt.Sprintf("订单 %v 可以取消。该操作会恢复库存，请点击确认按钮后执行。", result["orderId"])
	default:
		return "工具调用已完成。"
	}
}

func toolFailureMessage(value any) string {
	code, _ := value.(string)
	switch code {
	case "order not found":
		return "查询没有完成：未找到你名下的这个订单，请核对订单号。"
	case "product query failed", "order query failed":
		return "查询没有完成：服务暂时不可用，请稍后重试。"
	case "invalid arguments", "order_id must be a positive integer":
		return "查询没有完成：请求参数无效，请核对后重试。"
	case "only pending orders can be cancelled":
		return "操作没有完成：只有待处理订单可以取消。"
	case "unknown tool":
		return "操作没有完成：暂不支持此请求。"
	default:
		return "查询没有完成，请稍后重试。"
	}
}
