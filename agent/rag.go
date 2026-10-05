package agent

import (
	"context"
	"sort"
	"strings"
	"unicode"
)

type KnowledgeDocument struct {
	ID       string
	Title    string
	Category string
	Content  string
}

type KnowledgeChunk struct {
	DocumentID string  `json:"documentId"`
	Title      string  `json:"title"`
	Category   string  `json:"category"`
	Content    string  `json:"content"`
	Score      float64 `json:"score"`
}

type KnowledgeRetriever interface {
	Retrieve(context.Context, string, int) ([]KnowledgeChunk, error)
}

type LexicalRetriever struct{ documents []KnowledgeDocument }

type HybridRetriever struct{ documents []KnowledgeDocument }

func NewLexicalRetriever(documents []KnowledgeDocument) *LexicalRetriever {
	return &LexicalRetriever{documents: append([]KnowledgeDocument(nil), documents...)}
}

func NewHybridRetriever(documents []KnowledgeDocument) *HybridRetriever {
	return &HybridRetriever{documents: append([]KnowledgeDocument(nil), documents...)}
}

func DefaultKnowledgeRetriever() KnowledgeRetriever {
	return NewHybridRetriever(defaultKnowledgeDocuments())
}

func defaultKnowledgeDocuments() []KnowledgeDocument {
	return []KnowledgeDocument{
		{ID: "cancel-policy-v1", Title: "订单取消规则", Category: "售后", Content: "待处理订单可以申请取消，取消后恢复对应商品库存。已完成或已取消的订单不能重复取消。客服会先核验订单归属，并在执行前要求用户明确确认。"},
		{ID: "refund-policy-v1", Title: "退款处理说明", Category: "售后", Content: "订单取消成功后进入退款流程，具体到账时间取决于支付渠道。系统客服不能承诺具体到账时间，用户可以联系人工客服查询进度。"},
		{ID: "delivery-policy-v1", Title: "配送时效说明", Category: "配送", Content: "配送时间受商品库存、收货地区和承运商影响。当前客服可以查询订单状态，但不支持直接加急配送；需要加急时请转人工客服确认。"},
		{ID: "address-policy-v1", Title: "收货地址修改规则", Category: "订单", Content: "地址修改属于订单高风险操作，当前 Agent 不直接修改收货地址。订单尚未发货时可以转人工客服处理，已发货订单需要联系承运商或人工客服。"},
	}
}

func (r *LexicalRetriever) Retrieve(ctx context.Context, query string, topK int) ([]KnowledgeChunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if topK <= 0 {
		topK = 3
	}
	terms := tokenize(query)
	results := make([]KnowledgeChunk, 0, len(r.documents))
	for _, document := range r.documents {
		score := lexicalScore(terms, tokenize(document.Title+" "+document.Content))
		if score < 0.5 {
			continue
		}
		content := document.Content
		if len(content) > 600 {
			content = content[:600]
		}
		results = append(results, KnowledgeChunk{DocumentID: document.ID, Title: document.Title, Category: document.Category, Content: content, Score: score})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if len(results) > topK {
		results = results[:topK]
	}
	return results, nil
}

// Retrieve combines sparse term overlap with character n-gram similarity.
// The sparse leg handles exact policy terms; the n-gram leg handles Chinese
// wording variants without requiring an external embedding service.
func (r *HybridRetriever) Retrieve(ctx context.Context, query string, topK int) ([]KnowledgeChunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if topK <= 0 {
		topK = 3
	}
	query = normalizeKnowledgeQuery(query)
	if unsupportedKnowledgeQuery(query) {
		return []KnowledgeChunk{}, nil
	}
	terms := tokenize(query)
	queryNgrams := ngrams(query)
	results := make([]KnowledgeChunk, 0, len(r.documents))
	for _, document := range r.documents {
		text := normalizeKnowledgeQuery(document.Title + " " + document.Content)
		sparse := lexicalScore(terms, tokenize(text))
		dense := jaccard(queryNgrams, ngrams(text))
		score := 0.65*sparse + 0.35*dense
		if score < 0.25 {
			continue
		}
		content := document.Content
		if len(content) > 600 {
			content = content[:600]
		}
		results = append(results, KnowledgeChunk{DocumentID: document.ID, Title: document.Title, Category: document.Category, Content: content, Score: score})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].DocumentID < results[j].DocumentID
		}
		return results[i].Score > results[j].Score
	})
	if len(results) > topK {
		results = results[:topK]
	}
	return results, nil
}

func normalizeKnowledgeQuery(value string) string {
	replacements := map[string]string{"不想要": "取消", "退货": "退款", "钱多久": "退款到账", "快点送": "加急配送", "改收货地": "地址修改", "收货地点": "地址", "物流": "配送", "速度": "时效", "因素": "影响"}
	for from, to := range replacements {
		value = strings.ReplaceAll(value, from, to)
	}
	if strings.Contains(value, "取消") {
		value += " 待处理 订单"
	}
	if strings.Contains(value, "退款") {
		value += " 订单 到账"
	}
	if strings.Contains(value, "配送") {
		value += " 时效"
	}
	if strings.Contains(value, "地址") {
		value += " 收货 修改"
	}
	return value
}

func unsupportedKnowledgeQuery(value string) bool {
	for _, marker := range []string{"积分", "优惠券", "发票", "提示词", "内部规则", "别人的", "价格改"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func ngrams(value string) map[string]bool {
	runes := []rune(strings.ToLower(value))
	result := make(map[string]bool)
	for i := 0; i+1 < len(runes); i++ {
		result[string(runes[i:i+2])] = true
	}
	return result
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for term := range a {
		if b[term] {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	return float64(intersection) / float64(union)
}

func tokenize(value string) []string {
	value = strings.ToLower(value)
	terms := make([]string, 0, len(value))
	var ascii strings.Builder
	flush := func() {
		if ascii.Len() > 0 {
			terms = append(terms, ascii.String())
			ascii.Reset()
		}
	}
	for _, r := range value {
		if unicode.IsLetter(r) && r < unicode.MaxASCII {
			ascii.WriteRune(r)
			continue
		}
		flush()
		if unicode.Is(unicode.Han, r) {
			terms = append(terms, string(r))
		}
	}
	flush()
	return terms
}

func lexicalScore(query, document []string) float64 {
	if len(query) == 0 || len(document) == 0 {
		return 0
	}
	set := make(map[string]bool, len(document))
	for _, term := range document {
		set[term] = true
	}
	hits := 0
	for _, term := range query {
		if set[term] {
			hits++
		}
	}
	return float64(hits) / float64(len(query))
}
