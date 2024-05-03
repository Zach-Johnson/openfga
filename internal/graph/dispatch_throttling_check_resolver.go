package graph

import (
	"context"

	"go.opentelemetry.io/otel/attribute"

	"github.com/openfga/openfga/internal/server/config"
	"github.com/openfga/openfga/internal/throttler"
	"github.com/openfga/openfga/pkg/telemetry"
)

// DispatchThrottlingCheckResolverConfig encapsulates configuration for dispatch throttling check resolver.
type DispatchThrottlingCheckResolverConfig struct {
	DefaultThreshold uint32
	MaxThreshold     uint32
}

// DispatchThrottlingCheckResolver will prioritize requests with fewer dispatches over
// requests with more dispatches.
// Initially, request's dispatches will not be throttled and will be processed
// immediately. When the number of request dispatches is above the DefaultThreshold, the dispatches are placed
// in the throttling queue. One item form the throttling queue will be processed ticker.
// This allows a check / list objects request to be gradually throttled.
type DispatchThrottlingCheckResolver struct {
	delegate  CheckResolver
	config    *DispatchThrottlingCheckResolverConfig
	throttler throttler.Throttler
}

var _ CheckResolver = (*DispatchThrottlingCheckResolver)(nil)

// DispatchThrottlingCheckResolverOpt defines an option that can be used to change the behavior of DispatchThrottlingCheckResolver
// instance.
type DispatchThrottlingCheckResolverOpt func(checkResolver *DispatchThrottlingCheckResolver)

// WithDispatchThrottlingCheckResolverConfig sets the config to be used for DispatchThrottlingCheckResolver.
func WithDispatchThrottlingCheckResolverConfig(config DispatchThrottlingCheckResolverConfig) DispatchThrottlingCheckResolverOpt {
	return func(r *DispatchThrottlingCheckResolver) {
		r.config = &config
	}
}

// WithThrottler sets the throttler to be used for DispatchThrottlingCheckResolver.
func WithThrottler(throttler throttler.Throttler) DispatchThrottlingCheckResolverOpt {
	return func(r *DispatchThrottlingCheckResolver) {
		r.throttler = throttler
	}
}

func NewDispatchThrottlingCheckResolver(opts ...DispatchThrottlingCheckResolverOpt) *DispatchThrottlingCheckResolver {
	dispatchThrottlingCheckResolver := &DispatchThrottlingCheckResolver{
		config: &DispatchThrottlingCheckResolverConfig{
			DefaultThreshold: config.DefaultCheckDispatchThrottlingDefaultThreshold,
			MaxThreshold:     config.DefaultCheckDispatchThrottlingMaxThreshold,
		},
		throttler: throttler.NewNoopThrottler(),
	}
	dispatchThrottlingCheckResolver.delegate = dispatchThrottlingCheckResolver

	for _, opt := range opts {
		opt(dispatchThrottlingCheckResolver)
	}
	return dispatchThrottlingCheckResolver
}

func (r *DispatchThrottlingCheckResolver) SetDelegate(delegate CheckResolver) {
	r.delegate = delegate
}

func (r *DispatchThrottlingCheckResolver) GetDelegate() CheckResolver {
	return r.delegate
}

func (r *DispatchThrottlingCheckResolver) Close() {
	r.throttler.Close()
}

func (r *DispatchThrottlingCheckResolver) ResolveCheck(ctx context.Context,
	req *ResolveCheckRequest,
) (*ResolveCheckResponse, error) {
	ctx, span := tracer.Start(ctx, "ResolveCheck")
	defer span.End()
	span.SetAttributes(attribute.String("resolver_type", "DispatchThrottlingCheckResolver"))

	currentNumDispatch := req.GetRequestMetadata().DispatchCounter.Load()
	span.SetAttributes(attribute.Int("dispatch_count", int(currentNumDispatch)))

	threshold := r.config.DefaultThreshold

	maxThreshold := r.config.MaxThreshold
	if maxThreshold == 0 {
		maxThreshold = r.config.DefaultThreshold
	}

	thresholdInCtx := telemetry.DispatchThrottlingThresholdFromContext(ctx)

	if thresholdInCtx > 0 {
		threshold = min(thresholdInCtx, maxThreshold)
	}

	if currentNumDispatch > threshold {
		req.GetRequestMetadata().WasThrottled.Store(true)
		r.throttler.Throttle(ctx)
	}
	return r.delegate.ResolveCheck(ctx, req)
}
