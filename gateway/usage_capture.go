package gateway

import (
	"context"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

// usageCaptureWait bounds how long the trailing usage frame waits for the
// executor's raw-upstream usage record. Dispatch is a local goroutine, so this
// only ever pays out when the usage manager's queue is backed up. It runs after
// the client's last content token, so it never delays visible output.
const usageCaptureWait = 250 * time.Millisecond

const usageCapturePluginName = "tokenswim.gateway.capture"

type usageCaptureKey struct{}

// UsageCapture is one request's slot for the usage detail the executor parsed
// out of the RAW upstream stream, before any client-protocol translation
// (ADR 0019). Correlation is strictly by context: the plugin can only reach a
// slot through the context the request handed the executor, so concurrent
// requests can never observe each other's tokens.
type UsageCapture struct {
	model string

	mu     sync.Mutex
	detail usage.Detail
	have   bool

	once  sync.Once
	ready chan struct{}
}

var installUsageCapture sync.Once

// NewUsageCapture returns a context carrying a fresh capture slot, plus the slot.
func NewUsageCapture(ctx context.Context, model string) (context.Context, *UsageCapture) {
	// No explicit StartDefault: the usage manager self-starts its dispatcher
	// on the first Publish.
	installUsageCapture.Do(func() {
		usage.RegisterNamedPlugin(usageCapturePluginName, usageCapturePlugin{})
	})
	c := &UsageCapture{
		model: strings.TrimSpace(model),
		ready: make(chan struct{}),
	}
	return context.WithValue(ctx, usageCaptureKey{}, c), c
}

type usageCapturePlugin struct{}

func (usageCapturePlugin) HandleUsage(ctx context.Context, record usage.Record) {
	if c, ok := ctx.Value(usageCaptureKey{}).(*UsageCapture); ok {
		c.offer(record)
	}
}

// offer stores a published record. One execution can publish several — a codex
// request that ran the image tool publishes an auxiliary-model record after the
// main one — so once a record is held, only one for the request's own model may
// supersede it (terminal usage is cumulative, so the later one wins). If the
// executor normalized the model name so nothing ever matches the hint, this
// degrades to first-record-wins, which is still right: every path publishes the
// main record before any auxiliary-model one.
func (c *UsageCapture) offer(record usage.Record) {
	match := c.model != "" && strings.TrimSpace(record.Model) == c.model
	c.mu.Lock()
	if c.have && c.model != "" && !match {
		c.mu.Unlock()
		return
	}
	c.detail, c.have = record.Detail, true
	c.mu.Unlock()
	c.once.Do(func() { close(c.ready) })
}

// Wait blocks up to usageCaptureWait for the raw upstream detail. ok=false means
// no record arrived in time; the caller must then fall back to re-parsing the
// translated stream.
func (c *UsageCapture) Wait() (usage.Detail, bool) {
	if c == nil {
		return usage.Detail{}, false
	}
	timer := time.NewTimer(usageCaptureWait)
	defer timer.Stop()
	select {
	case <-c.ready:
	case <-timer.C:
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.detail, c.have
}

// ResolveUsage picks the detail the tokenswim.usage frame (and the non-stream
// usage header) is built from: the raw upstream detail when it arrived,
// otherwise the re-parse of the translated client stream. A raw record with no
// tokens is treated as no record — failure publications carry an empty detail
// and must never zero a request the translated stream can still account for.
func ResolveUsage(capture *UsageCapture, reparsed usage.Detail, haveReparsed bool, model string) (usage.Detail, bool) {
	raw, ok := capture.Wait()
	if !ok || !usageHasTokens(raw) {
		return reparsed, haveReparsed
	}
	if haveReparsed {
		logUsageDivergence(model, raw, reparsed)
	}
	return raw, true
}

// logUsageDivergence is the translator-fidelity sentinel that replaces ADR
// 0014's "translator fidelity is a hard precondition for settlement": settlement
// now follows raw upstream, and a translated stream that disagrees is a defect
// to report upstream rather than a billing corruption to discover later.
//
// Debug, not Warn: the claude wire protocol has no slot for reasoning and
// reports input net of cache read, so this fires on EVERY /v1/messages request
// until upstream fixes the wire format. At Warn it would bury real warnings.
// Since settlement no longer depends on translator fidelity, this is diagnostic
// data, not an alert.
func logUsageDivergence(model string, raw, translated usage.Detail) {
	if raw.InputTokens == translated.InputTokens &&
		raw.OutputTokens == translated.OutputTokens &&
		raw.CacheReadTokens == translated.CacheReadTokens &&
		raw.CacheCreationTokens == translated.CacheCreationTokens &&
		raw.ReasoningTokens == translated.ReasoningTokens {
		return
	}
	log.WithFields(log.Fields{
		"model":           model,
		"raw_input":       raw.InputTokens,
		"raw_output":      raw.OutputTokens,
		"raw_cache_read":  raw.CacheReadTokens,
		"raw_cache_write": raw.CacheCreationTokens,
		"raw_reasoning":   raw.ReasoningTokens,
		"tr_input":        translated.InputTokens,
		"tr_output":       translated.OutputTokens,
		"tr_cache_read":   translated.CacheReadTokens,
		"tr_cache_write":  translated.CacheCreationTokens,
		"tr_reasoning":    translated.ReasoningTokens,
	}).Debug("gateway: translated usage disagrees with raw upstream usage; settling on raw")
}
