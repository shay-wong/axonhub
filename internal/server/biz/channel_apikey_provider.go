package biz

import (
	"context"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"sort"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm/auth"
)

// traceStickyLRUSize is the default LRU cache size for trace-to-key mappings.
const traceStickyLRUSize = 1024

const (
	APIKeySelectionStrategyTraceSticky    = "trace_sticky"
	APIKeySelectionStrategyWeightedSticky = "weighted_sticky"
	APIKeySelectionStrategyFailover       = "failover"
)

func NewChannelAPIKeyContextProvider(inner auth.APIKeyProvider) auth.APIKeyProvider {
	return NewRecordingAPIKeyProvider(inner)
}

// TraceStickyKeyProvider selects an API key deterministically per traceID (if present),
// using cached enabled keys from the channel snapshot.
//
// An LRU cache remembers previous traceID→key selections so that, as long as
// the previously chosen key is still enabled, the same key is returned even when
// the enabled-key set changes (e.g. a new key is added). This improves sticky
// stability compared to pure rendezvous hashing alone.
//
//nolint:revive // exported for use in transformers via interface.
type TraceStickyKeyProvider struct {
	channel *Channel
	cache   *lru.Cache[string, string]
}

type WeightedTraceStickyKeyProvider struct {
	channel *Channel
	cache   *lru.Cache[string, string]
}

type FailoverAPIKeyProvider struct {
	channel *Channel
	cache   *lru.Cache[string, string]
}

type recordingAPIKeyProvider struct {
	provider auth.APIKeyProvider
}

func NewRecordingAPIKeyProvider(provider auth.APIKeyProvider) auth.APIKeyProvider {
	return &recordingAPIKeyProvider{provider: provider}
}

func (p *recordingAPIKeyProvider) Get(ctx context.Context) string {
	key := p.provider.Get(ctx)
	recordSelectedAPIKey(ctx, key)

	return key
}

func NewTraceStickyKeyProvider(channel *Channel) *TraceStickyKeyProvider {
	cache, _ := lru.New[string, string](traceStickyLRUSize)

	return &TraceStickyKeyProvider{
		channel: channel,
		cache:   cache,
	}
}

func NewWeightedTraceStickyKeyProvider(channel *Channel) *WeightedTraceStickyKeyProvider {
	cache, _ := lru.New[string, string](traceStickyLRUSize)

	return &WeightedTraceStickyKeyProvider{
		channel: channel,
		cache:   cache,
	}
}

func NewFailoverAPIKeyProvider(channel *Channel) *FailoverAPIKeyProvider {
	cache, _ := lru.New[string, string](traceStickyLRUSize)

	return &FailoverAPIKeyProvider{
		channel: channel,
		cache:   cache,
	}
}

func recordSelectedAPIKey(ctx context.Context, key string) {
	if key == "" {
		return
	}

	contexts.WithChannelAPIKey(ctx, key)
}

func (p *TraceStickyKeyProvider) Get(ctx context.Context) string {
	enabled := apiKeyConfigKeys(availableAPIKeyConfigs(ctx, p.channel))
	if len(enabled) == 0 {
		panicNoEnabledAPIKey(p.channel)
	}

	if len(enabled) == 1 {
		logSelectedAPIKey(ctx, "Single key selected", p.channel, enabled[0])
		recordSelectedAPIKey(ctx, enabled[0])

		return enabled[0]
	}

	var selectedKey string

	if trace, ok := contexts.GetTrace(ctx); ok && trace != nil {
		if cached, ok := p.cache.Get(trace.TraceID); ok && containsKey(enabled, cached) {
			selectedKey = cached
		} else {
			selectedKey = rendezvousSelect(enabled, trace.TraceID)
			p.cache.Add(trace.TraceID, selectedKey)
		}

		logSelectedAPIKey(ctx, "Trace sticky key selected", p.channel, selectedKey, log.String("trace_id", trace.TraceID))
	} else {
		//nolint:gosec // not a security issue, just a random selection.
		selectedKey = enabled[rand.IntN(len(enabled))]
		logSelectedAPIKey(ctx, "Random key selected", p.channel, selectedKey)
	}

	recordSelectedAPIKey(ctx, selectedKey)

	return selectedKey
}

func (p *WeightedTraceStickyKeyProvider) Get(ctx context.Context) string {
	enabled := availableAPIKeyConfigs(ctx, p.channel)
	if len(enabled) == 0 {
		panicNoEnabledAPIKey(p.channel)
	}

	var selectedKey string
	if trace, ok := contexts.GetTrace(ctx); ok && trace != nil {
		if cached, ok := p.cache.Get(trace.TraceID); ok && containsAPIKeyConfig(enabled, cached) {
			selectedKey = cached
		} else {
			selectedKey = weightedRendezvousSelect(enabled, trace.TraceID)
			p.cache.Add(trace.TraceID, selectedKey)
		}
	} else {
		selectedKey = weightedRandomSelect(enabled)
	}
	if trace, ok := contexts.GetTrace(ctx); ok && trace != nil {
		logSelectedAPIKey(ctx, "Weighted trace sticky key selected", p.channel, selectedKey, log.String("trace_id", trace.TraceID))
	} else {
		logSelectedAPIKey(ctx, "Weighted random key selected", p.channel, selectedKey)
	}

	recordSelectedAPIKey(ctx, selectedKey)

	return selectedKey
}

func (p *FailoverAPIKeyProvider) Get(ctx context.Context) string {
	enabled := availableAPIKeyConfigs(ctx, p.channel)
	if len(enabled) == 0 {
		panicNoEnabledAPIKey(p.channel)
	}

	topWeight := enabled[0].Weight
	for _, config := range enabled[1:] {
		if config.Weight > topWeight {
			topWeight = config.Weight
		}
	}

	topConfigs := make([]objects.ChannelAPIKeyConfig, 0, len(enabled))
	for _, config := range enabled {
		if config.Weight == topWeight {
			topConfigs = append(topConfigs, config)
		}
	}

	keys := apiKeyConfigKeys(topConfigs)
	var selectedKey string
	if trace, ok := contexts.GetTrace(ctx); ok && trace != nil {
		if cached, ok := p.cache.Get(trace.TraceID); ok && containsKey(keys, cached) {
			selectedKey = cached
		} else {
			selectedKey = rendezvousSelect(keys, trace.TraceID)
			p.cache.Add(trace.TraceID, selectedKey)
		}
	} else {
		//nolint:gosec // not a security issue, just a random selection.
		selectedKey = keys[rand.IntN(len(keys))]
	}
	if trace, ok := contexts.GetTrace(ctx); ok && trace != nil {
		logSelectedAPIKey(ctx, "Failover trace sticky key selected", p.channel, selectedKey, log.String("trace_id", trace.TraceID))
	} else {
		logSelectedAPIKey(ctx, "Failover random key selected", p.channel, selectedKey)
	}

	recordSelectedAPIKey(ctx, selectedKey)

	return selectedKey
}

func availableAPIKeyConfigs(ctx context.Context, channel *Channel) []objects.ChannelAPIKeyConfig {
	return lo.Filter(channel.GetEnabledAPIKeyConfigs(), func(config objects.ChannelAPIKeyConfig, _ int) bool {
		return !contexts.IsChannelAPIKeyExcluded(ctx, channel.ID, config.Key)
	})
}

// rendezvousSelect picks a key using Highest Random Weight (Rendezvous) hashing.
// This is stable when the key set changes (minimal remapping compared to modulo).
func rendezvousSelect(keys []string, seed string) string {
	bestKey := keys[0]
	bestScore := hashAPIKey(seed + "|" + bestKey)

	for i := 1; i < len(keys); i++ {
		k := keys[i]

		s := hashAPIKey(seed + "|" + k)
		if s > bestScore {
			bestScore = s
			bestKey = k
		}
	}

	return bestKey
}

func weightedRendezvousSelect(configs []objects.ChannelAPIKeyConfig, seed string) string {
	bestConfig := configs[0]
	bestScore := weightedRendezvousScore(seed, bestConfig)

	for i := 1; i < len(configs); i++ {
		score := weightedRendezvousScore(seed, configs[i])
		if score > bestScore {
			bestScore = score
			bestConfig = configs[i]
		}
	}

	return bestConfig.Key
}

func weightedRendezvousScore(seed string, config objects.ChannelAPIKeyConfig) float64 {
	weight := config.Weight
	if weight <= 0 {
		weight = 100
	}

	const maxUint64Float = float64(^uint64(0))

	hashValue := hashAPIKey(seed + "|" + config.Key)
	u := (float64(hashValue) + 1) / (maxUint64Float + 1)
	if u <= 0 {
		u = math.SmallestNonzeroFloat64
	}

	return math.Log(u) / float64(weight)
}

func weightedRandomSelect(configs []objects.ChannelAPIKeyConfig) string {
	total := 0
	for _, config := range configs {
		total += config.Weight
	}
	if total <= 0 {
		//nolint:gosec // not a security issue, just a random selection.
		return configs[rand.IntN(len(configs))].Key
	}

	//nolint:gosec // not a security issue, just a weighted random selection.
	target := rand.IntN(total)
	for _, config := range configs {
		target -= config.Weight
		if target < 0 {
			return config.Key
		}
	}

	return configs[len(configs)-1].Key
}

func containsAPIKeyConfig(configs []objects.ChannelAPIKeyConfig, key string) bool {
	for _, config := range configs {
		if config.Key == key {
			return true
		}
	}

	return false
}

func apiKeyConfigKeys(configs []objects.ChannelAPIKeyConfig) []string {
	keys := make([]string, 0, len(configs))
	for _, config := range configs {
		keys = append(keys, config.Key)
	}
	sort.Strings(keys)

	return keys
}

func containsKey(keys []string, key string) bool {
	for _, current := range keys {
		if current == key {
			return true
		}
	}

	return false
}

func hashAPIKey(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))

	return h.Sum64()
}

func panicNoEnabledAPIKey(ch *Channel) {
	if ch == nil {
		panic("no enabled api key configured")
	}

	panic("no enabled api key configured for channel " + ch.Name)
}

func logSelectedAPIKey(ctx context.Context, message string, channel *Channel, key string, fields ...log.Field) {
	if !log.DebugEnabled(ctx) {
		return
	}

	fields = append(fields, apiKeyIdentityLogFields(channel.APIKeyIdentity(key))...)
	log.Debug(ctx, message, fields...)
}
