package usage

import "strings"

// price is USD per million tokens.
type price struct {
	in, out, cacheRead float64
}

// Ordered most-specific first; matched by substring on the model id.
// Cache writes are billed at 1.25x input (5m TTL) / 2x input (1h TTL).
var priceTable = []struct {
	match string
	p     price
}{
	{"fable-5-1", price{10, 50, 0.25}},
	{"mythos-5-1", price{10, 50, 0.25}},
	{"fable", price{10, 50, 1.0}},
	{"mythos", price{10, 50, 1.0}},
	{"opus-5-5", price{4, 20, 0.20}},
	{"opus-5", price{5, 25, 0.50}},
	{"opus-4-8", price{5, 25, 0.50}},
	{"opus-4-7", price{5, 25, 0.50}},
	{"opus-4-6", price{5, 25, 0.50}},
	{"opus-4-5", price{5, 25, 0.50}},
	{"opus", price{15, 75, 1.50}},
	{"sonnet-5", price{2, 10, 0.20}},
	{"sonnet", price{3, 15, 0.30}},
	{"haiku-5", price{0.10, 0.50, 0.01}},
	{"haiku-4", price{1, 5, 0.10}},
	{"haiku", price{0.80, 4, 0.08}},
}

func lookupPrice(model string) (price, bool) {
	for _, e := range priceTable {
		if strings.Contains(model, e.match) {
			return e.p, true
		}
	}
	return price{}, false
}

// Cost returns the API-equivalent USD cost of a response.
// cacheW1h is the portion of t.CacheW written with the 1-hour TTL.
func Cost(model string, t Tokens, cacheW1h int64) float64 {
	p, ok := lookupPrice(model)
	if !ok {
		return 0
	}
	if cacheW1h > t.CacheW {
		cacheW1h = t.CacheW
	}
	w5m := t.CacheW - cacheW1h
	usd := float64(t.In)*p.in +
		float64(t.Out)*p.out +
		float64(t.CacheR)*p.cacheRead +
		float64(w5m)*p.in*1.25 +
		float64(cacheW1h)*p.in*2
	return usd / 1e6
}
