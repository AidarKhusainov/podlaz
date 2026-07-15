package tundiag

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"time"
)

type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

type ProbeDefinition struct {
	ID         string
	Layer      Layer
	Target     string
	Timeout    time.Duration
	DependsOn  []string
	BestEffort bool
}

type Probe struct {
	Definition ProbeDefinition
	Run        func(context.Context) ProbeResult
}

type Runner struct {
	Clock Clock
}

func (r Runner) Run(ctx context.Context, base Report, probes []Probe) Report {
	clock := r.Clock
	if clock == nil {
		clock = systemClock{}
	}
	if base.SchemaVersion == 0 {
		base.SchemaVersion = SchemaVersion
	}
	if base.GeneratedAt.IsZero() {
		base.GeneratedAt = clock.Now()
	}
	base.Probes = make([]ProbeResult, 0, len(probes))
	seen := make(map[string]ProbeResult, len(probes))

	for _, probe := range probes {
		definition := normalizeDefinition(probe.Definition)
		if err := validateDefinition(definition, seen); err != nil {
			result := ProbeResult{
				ID:             fallbackProbeID(definition.ID),
				Layer:          fallbackLayer(definition.Layer),
				Status:         ProbeFail,
				TimeoutMS:      definition.Timeout.Milliseconds(),
				Target:         definition.Target,
				Classification: ClassInternalDiagnosticError,
				Error:          err.Error(),
			}
			base.Probes = append(base.Probes, result)
			seen[result.ID] = result
			continue
		}

		if reason := dependencyFailureReason(definition.DependsOn, seen); reason != "" {
			result := skippedResult(definition, reason, "")
			base.Probes = append(base.Probes, result)
			seen[result.ID] = result
			continue
		}
		if err := ctx.Err(); err != nil {
			result := skippedResult(definition, "diagnostic run cancelled before probe started", cancellationClassification(err))
			base.Probes = append(base.Probes, result)
			seen[result.ID] = result
			continue
		}
		if probe.Run == nil {
			result := ProbeResult{
				ID:             definition.ID,
				Layer:          definition.Layer,
				Status:         ProbeFail,
				TimeoutMS:      definition.Timeout.Milliseconds(),
				Target:         definition.Target,
				Classification: ClassInternalDiagnosticError,
				Error:          "probe runner is not configured",
			}
			base.Probes = append(base.Probes, result)
			seen[result.ID] = result
			continue
		}

		started := clock.Now()
		probeCtx, cancel := context.WithTimeout(ctx, definition.Timeout)
		result := runSafely(probeCtx, probe.Run)
		probeErr := probeCtx.Err()
		cancel()

		result.ID = definition.ID
		result.Layer = definition.Layer
		result.Target = firstNonEmpty(result.Target, definition.Target)
		result.TimeoutMS = definition.Timeout.Milliseconds()
		result.DurationMS = nonNegativeMilliseconds(clock.Now().Sub(started))
		if result.Status == "" {
			result.Status = ProbePass
		}
		if probeErr != nil {
			result.Status = ProbeFail
			result.Classification = cancellationClassification(probeErr)
			result.Error = probeErr.Error()
		}
		if result.Status == ProbeFail && result.Classification == "" {
			if probeErr != nil {
				result.Classification = cancellationClassification(probeErr)
			} else {
				result.Classification = ClassInternalDiagnosticError
			}
		}
		result = SanitizeProbeResult(result)
		base.Probes = append(base.Probes, result)
		seen[result.ID] = result
	}

	return Finalize(base)
}

func normalizeDefinition(definition ProbeDefinition) ProbeDefinition {
	definition.ID = strings.TrimSpace(definition.ID)
	definition.Target = strings.TrimSpace(definition.Target)
	if definition.Timeout <= 0 {
		definition.Timeout = 3 * time.Second
	}
	return definition
}

func validateDefinition(definition ProbeDefinition, seen map[string]ProbeResult) error {
	if definition.ID == "" {
		return fmt.Errorf("probe id is required")
	}
	if definition.Layer == "" {
		return fmt.Errorf("probe %s layer is required", definition.ID)
	}
	if _, duplicate := seen[definition.ID]; duplicate {
		return fmt.Errorf("duplicate probe id %s", definition.ID)
	}
	for _, dependency := range definition.DependsOn {
		if _, ok := seen[dependency]; !ok {
			return fmt.Errorf("probe %s depends on unknown or later probe %s", definition.ID, dependency)
		}
	}
	return nil
}

func dependencyFailureReason(dependencies []string, seen map[string]ProbeResult) string {
	for _, dependency := range dependencies {
		result := seen[dependency]
		if result.Status != ProbePass {
			return fmt.Sprintf("dependency %s status is %s", dependency, result.Status)
		}
	}
	return ""
}

func skippedResult(definition ProbeDefinition, reason string, classification Classification) ProbeResult {
	return SanitizeProbeResult(ProbeResult{
		ID:               definition.ID,
		Layer:            definition.Layer,
		Status:           ProbeSkipped,
		TimeoutMS:        definition.Timeout.Milliseconds(),
		Target:           definition.Target,
		Classification:   classification,
		DependencyReason: reason,
	})
}

func runSafely(ctx context.Context, run func(context.Context) ProbeResult) (result ProbeResult) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = ProbeResult{
				Status:         ProbeFail,
				Classification: ClassInternalDiagnosticError,
				Error:          fmt.Sprintf("probe panic: %v", recovered),
				Evidence: Evidence{Notes: []string{
					limitText(string(debug.Stack()), 2048),
				}},
			}
		}
	}()
	return run(ctx)
}

func cancellationClassification(err error) Classification {
	if err == context.DeadlineExceeded {
		return ClassTimeout
	}
	return ClassCancelled
}

func fallbackProbeID(id string) string {
	if strings.TrimSpace(id) == "" {
		return "invalid-probe"
	}
	return id
}

func fallbackLayer(layer Layer) Layer {
	if layer == "" {
		return LayerSession
	}
	return layer
}

func nonNegativeMilliseconds(duration time.Duration) int64 {
	if duration < 0 {
		return 0
	}
	return duration.Milliseconds()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
