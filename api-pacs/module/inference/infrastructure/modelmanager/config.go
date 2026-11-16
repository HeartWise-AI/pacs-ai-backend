package modelmanager

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Config describes one host manager with one or more GPU budgets. BudgetMiB includes SafetyMarginMiB.
type Config struct {
	Enabled bool
	// RetainPeakWhileLoaded protects against allocator caches surviving inference.
	RetainPeakWhileLoaded bool
	GPUReservationPercent int
	RequireReconciliation bool
	BudgetMiB             int
	SafetyMarginMiB       int
	QueueTimeout          time.Duration
	OperationTimeout      time.Duration
	// GPUBudgetsMiB replaces the legacy single-device budget when set. Keys
	// are physical GPU UUIDs, never CUDA or nvidia-smi ordinals.
	GPUBudgetsMiB     map[string]int
	IdleSweepInterval time.Duration
	// EvictionPolicy selects which idle models a waiting request may evict:
	// EvictionIdleTimeout (default) only models past their idle timeout,
	// EvictionLRUOnDemand the least recently used idle model once it has been
	// resident for MinResidency.
	EvictionPolicy string
	MinResidency   time.Duration
	// MeasureInterval is the period of the per-process GPU memory sampler; 0 disables it.
	MeasureInterval time.Duration
	// MeasureStatePath stores observations in a JSON file instead of Postgres.
	MeasureStatePath string
	// ReservationSource selects declared image metadata (default) or measured
	// peaks once MeasuredMinSamples predictions have been observed.
	ReservationSource     string
	MeasuredMinSamples    int
	MeasuredSafetyPercent int
	// MeasuredMinBusy is how far into a prediction a sample must be taken for
	// that prediction to count as observed (two samples also count).
	MeasuredMinBusy time.Duration
	// PlacementMode: pinned (default) never plans, propose only serves the plan,
	// auto also applies at most one move per PlacementInterval.
	PlacementMode     string
	PlacementInterval time.Duration
	// PlacementMinIdle is how long a loaded model must have been idle before
	// the automatic loop may redeploy it; an unloaded model may always move.
	PlacementMinIdle time.Duration
	// PlacementWindow restricts automatic moves to a daily local-time window
	// "HH:MM-HH:MM" (it may cross midnight); empty allows any time.
	PlacementWindow string
	// AllowedGPUUUIDs limits the GPUs a plan may move a model to; empty means all configured GPUs.
	AllowedGPUUUIDs []string
	// PlacementTenantID and PlacementActor identify the registrations the
	// automatic loop may move and the actor recorded on its deployments.
	PlacementTenantID string
	PlacementActor    string
}

const (
	EvictionIdleTimeout = "idle-timeout"
	EvictionLRUOnDemand = "lru-on-demand"

	ReservationDeclared = "declared"
	ReservationMeasured = "measured"

	PlacementPinned  = "pinned"
	PlacementPropose = "propose"
	PlacementAuto    = "auto"

	defaultMeasuredMinSamples    = 10
	defaultMeasuredSafetyPercent = 125
	defaultMeasuredMinBusy       = time.Second
	defaultPlacementMinIdle      = 900 * time.Second
	defaultPlacementActor        = "system:gpu-auto-placement"
)

func ConfigFromEnv(getenv func(string) string) (Config, error) {
	c := Config{SafetyMarginMiB: 1024, QueueTimeout: 10 * time.Minute, OperationTimeout: 5 * time.Minute, EvictionPolicy: EvictionIdleTimeout, MinResidency: 30 * time.Second,
		MeasureInterval: 5 * time.Second, ReservationSource: ReservationDeclared, MeasuredMinSamples: defaultMeasuredMinSamples, MeasuredSafetyPercent: defaultMeasuredSafetyPercent,
		MeasuredMinBusy: defaultMeasuredMinBusy, PlacementMode: PlacementPinned, PlacementInterval: 300 * time.Second, PlacementMinIdle: defaultPlacementMinIdle, PlacementActor: defaultPlacementActor}
	if v := getenv("MODEL_MANAGER_ENABLED"); v != "" {
		var err error
		c.Enabled, err = strconv.ParseBool(v)
		if err != nil {
			return c, fmt.Errorf("MODEL_MANAGER_ENABLED: %w", err)
		}
	}
	if !c.Enabled {
		return c, nil
	}
	c.RequireReconciliation = true
	c.RetainPeakWhileLoaded = true
	c.GPUReservationPercent = 100
	if v := getenv("MODEL_RETAIN_PEAK_WHILE_LOADED"); v != "" {
		var err error
		c.RetainPeakWhileLoaded, err = strconv.ParseBool(v)
		if err != nil {
			return c, fmt.Errorf("MODEL_RETAIN_PEAK_WHILE_LOADED: %w", err)
		}
	}
	if v := getenv("GPU_MEMORY_BUDGETS_MIB"); v != "" {
		if err := json.Unmarshal([]byte(v), &c.GPUBudgetsMiB); err != nil || len(c.GPUBudgetsMiB) == 0 {
			return c, fmt.Errorf("GPU_MEMORY_BUDGETS_MIB must be a nonempty JSON object of GPU UUIDs to MiB budgets")
		}
	}
	if v := getenv("MODEL_IDLE_SWEEP_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return c, fmt.Errorf("MODEL_IDLE_SWEEP_SECONDS must be a non-negative integer")
		}
		c.IdleSweepInterval = time.Duration(n) * time.Second
	}
	for _, field := range []struct {
		name   string
		target *int
	}{
		{"GPU_MEMORY_BUDGET_MIB", &c.BudgetMiB},
		{"MODEL_GPU_MEMORY_RESERVATION_PERCENT", &c.GPUReservationPercent},
		{"GPU_MEMORY_SAFETY_MARGIN_MIB", &c.SafetyMarginMiB},
	} {
		if v := getenv(field.name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return c, fmt.Errorf("%s: %w", field.name, err)
			}
			*field.target = n
		}
	}
	if v := getenv("MODEL_QUEUE_TIMEOUT_SECONDS"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n <= 0 {
			return c, fmt.Errorf("MODEL_QUEUE_TIMEOUT_SECONDS must be a positive integer")
		}
		c.QueueTimeout = time.Duration(n) * time.Second
	}
	if v := getenv("MODEL_EVICTION_POLICY"); v != "" {
		c.EvictionPolicy = v
	}
	if v := getenv("MODEL_MIN_RESIDENCY_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return c, fmt.Errorf("MODEL_MIN_RESIDENCY_SECONDS must be a non-negative integer")
		}
		c.MinResidency = time.Duration(n) * time.Second
	}
	if c.GPUReservationPercent < 100 {
		return c, fmt.Errorf("MODEL_GPU_MEMORY_RESERVATION_PERCENT must be between 100 and 1000")
	}
	if v := getenv("MODEL_GPU_MEASURE_INTERVAL_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 3600 {
			return c, fmt.Errorf("MODEL_GPU_MEASURE_INTERVAL_SECONDS must be an integer between 0 and 3600 (0 disables measurement)")
		}
		c.MeasureInterval = time.Duration(n) * time.Second
	}
	c.MeasureStatePath = strings.TrimSpace(getenv("MODEL_GPU_MEASURE_STATE_PATH"))
	if v := getenv("MODEL_RESERVATION_SOURCE"); v != "" {
		c.ReservationSource = v
	}
	if v := getenv("MODEL_MEASURED_MIN_SAMPLES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return c, fmt.Errorf("MODEL_MEASURED_MIN_SAMPLES must be a positive integer")
		}
		c.MeasuredMinSamples = n
	}
	if v := getenv("MODEL_MEASURED_SAFETY_PERCENT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 100 || n > 1000 {
			return c, fmt.Errorf("MODEL_MEASURED_SAFETY_PERCENT must be an integer between 100 and 1000")
		}
		c.MeasuredSafetyPercent = n
	}
	if v := getenv("MODEL_MEASURED_MIN_BUSY_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 3600 {
			return c, fmt.Errorf("MODEL_MEASURED_MIN_BUSY_SECONDS must be an integer between 0 and 3600")
		}
		// Zero accepts any sample taken during a prediction.
		c.MeasuredMinBusy = max(time.Duration(n)*time.Second, time.Nanosecond)
	}
	if v := getenv("MODEL_GPU_PLACEMENT"); v != "" {
		c.PlacementMode = v
	}
	if v := getenv("MODEL_GPU_PLACEMENT_MIN_IDLE_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 604800 {
			return c, fmt.Errorf("MODEL_GPU_PLACEMENT_MIN_IDLE_SECONDS must be an integer between 0 and 604800")
		}
		c.PlacementMinIdle = max(time.Duration(n)*time.Second, time.Nanosecond)
	}
	c.PlacementWindow = strings.TrimSpace(getenv("MODEL_GPU_PLACEMENT_WINDOW"))
	if v := getenv("MODEL_GPU_PLACEMENT_INTERVAL_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 30 || n > 86400 {
			return c, fmt.Errorf("MODEL_GPU_PLACEMENT_INTERVAL_SECONDS must be an integer between 30 and 86400")
		}
		c.PlacementInterval = time.Duration(n) * time.Second
	}
	if v := strings.TrimSpace(getenv("MODEL_GPU_ALLOWED_UUIDS")); v != "" {
		for _, uuid := range strings.Split(v, ",") {
			if uuid = strings.TrimSpace(uuid); uuid != "" {
				c.AllowedGPUUUIDs = append(c.AllowedGPUUUIDs, uuid)
			}
		}
		if len(c.AllowedGPUUUIDs) == 0 {
			return c, fmt.Errorf("MODEL_GPU_ALLOWED_UUIDS must be a comma-separated list of physical GPU UUIDs")
		}
	}
	c.PlacementTenantID = strings.TrimSpace(getenv("MODEL_GPU_PLACEMENT_TENANT_ID"))
	if v := strings.TrimSpace(getenv("MODEL_GPU_PLACEMENT_ACTOR")); v != "" {
		c.PlacementActor = v
	}
	if c.ReservationSource == ReservationMeasured && c.MeasureInterval == 0 {
		return c, fmt.Errorf("MODEL_RESERVATION_SOURCE=measured requires MODEL_GPU_MEASURE_INTERVAL_SECONDS greater than 0")
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if c.GPUReservationPercent != 0 && (c.GPUReservationPercent < 100 || c.GPUReservationPercent > 1000) {
		return fmt.Errorf("MODEL_GPU_MEMORY_RESERVATION_PERCENT must be between 100 and 1000")
	}
	if c.IdleSweepInterval < 0 {
		return fmt.Errorf("idle sweep interval must be non-negative")
	}
	if c.EvictionPolicy != "" && c.EvictionPolicy != EvictionIdleTimeout && c.EvictionPolicy != EvictionLRUOnDemand {
		return fmt.Errorf("MODEL_EVICTION_POLICY must be %q or %q", EvictionIdleTimeout, EvictionLRUOnDemand)
	}
	if c.MinResidency < 0 {
		return fmt.Errorf("minimum residency must be non-negative")
	}
	if c.MeasureInterval < 0 {
		return fmt.Errorf("MODEL_GPU_MEASURE_INTERVAL_SECONDS must be non-negative")
	}
	if c.ReservationSource != "" && c.ReservationSource != ReservationDeclared && c.ReservationSource != ReservationMeasured {
		return fmt.Errorf("MODEL_RESERVATION_SOURCE must be %q or %q", ReservationDeclared, ReservationMeasured)
	}
	if c.MeasuredMinSamples < 0 {
		return fmt.Errorf("MODEL_MEASURED_MIN_SAMPLES must be a positive integer")
	}
	if c.MeasuredSafetyPercent != 0 && (c.MeasuredSafetyPercent < 100 || c.MeasuredSafetyPercent > 1000) {
		return fmt.Errorf("MODEL_MEASURED_SAFETY_PERCENT must be an integer between 100 and 1000")
	}
	if c.MeasuredMinBusy < 0 || c.PlacementMinIdle < 0 {
		return fmt.Errorf("MODEL_MEASURED_MIN_BUSY_SECONDS and MODEL_GPU_PLACEMENT_MIN_IDLE_SECONDS must be non-negative")
	}
	if _, _, err := ParsePlacementWindow(c.PlacementWindow); err != nil {
		return err
	}
	switch c.PlacementMode {
	case "", PlacementPinned:
	case PlacementPropose, PlacementAuto:
		if len(c.GPUBudgetsMiB) == 0 {
			return fmt.Errorf("MODEL_GPU_PLACEMENT=%s requires per-GPU budgets in GPU_MEMORY_BUDGETS_MIB", c.PlacementMode)
		}
		if c.PlacementMode == PlacementAuto && (c.PlacementInterval <= 0 || c.PlacementTenantID == "") {
			return fmt.Errorf("MODEL_GPU_PLACEMENT=auto requires MODEL_GPU_PLACEMENT_TENANT_ID and a positive MODEL_GPU_PLACEMENT_INTERVAL_SECONDS")
		}
	default:
		return fmt.Errorf("MODEL_GPU_PLACEMENT must be %q, %q or %q", PlacementPinned, PlacementPropose, PlacementAuto)
	}
	seenAllowed := map[string]struct{}{}
	for _, uuid := range c.AllowedGPUUUIDs {
		if _, configured := c.GPUBudgetsMiB[uuid]; !gpuUUIDPattern.MatchString(uuid) || !configured {
			return fmt.Errorf("MODEL_GPU_ALLOWED_UUIDS entry %q is not a GPU UUID configured in GPU_MEMORY_BUDGETS_MIB", uuid)
		}
		if _, duplicate := seenAllowed[uuid]; duplicate {
			return fmt.Errorf("MODEL_GPU_ALLOWED_UUIDS lists %q more than once", uuid)
		}
		seenAllowed[uuid] = struct{}{}
	}
	if len(c.GPUBudgetsMiB) > 0 {
		for uuid, budget := range c.GPUBudgetsMiB {
			if !gpuUUIDPattern.MatchString(uuid) || c.SafetyMarginMiB < 0 || budget <= c.SafetyMarginMiB {
				return fmt.Errorf("each GPU UUID budget must exceed its non-negative safety margin")
			}
		}
	} else if c.BudgetMiB <= 0 || c.SafetyMarginMiB < 0 || c.SafetyMarginMiB >= c.BudgetMiB {
		return fmt.Errorf("GPU budget must exceed its non-negative safety margin")
	}
	if c.QueueTimeout <= 0 || c.OperationTimeout <= 0 {
		return fmt.Errorf("manager timeouts must be positive")
	}
	return nil
}

var gpuUUIDPattern = regexp.MustCompile(`^GPU-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ParsePlacementWindow reads "HH:MM-HH:MM" into minutes after midnight. An
// empty value means no restriction and returns equal bounds.
func ParsePlacementWindow(value string) (start, end int, err error) {
	if value == "" {
		return 0, 0, nil
	}
	invalid := fmt.Errorf("MODEL_GPU_PLACEMENT_WINDOW must look like 22:00-06:00 (24-hour local time, different start and end)")
	parts := strings.Split(value, "-")
	if len(parts) != 2 {
		return 0, 0, invalid
	}
	bounds := [2]int{}
	for i, part := range parts {
		clock, parseErr := time.Parse("15:04", strings.TrimSpace(part))
		if parseErr != nil {
			return 0, 0, invalid
		}
		bounds[i] = clock.Hour()*60 + clock.Minute()
	}
	if bounds[0] == bounds[1] {
		return 0, 0, invalid
	}
	return bounds[0], bounds[1], nil
}

// InPlacementWindow reports whether the wall-clock time of at, in its own
// location, falls inside the window. A window may cross midnight.
func InPlacementWindow(value string, at time.Time) bool {
	start, end, err := ParsePlacementWindow(value)
	if err != nil || start == end {
		return err == nil
	}
	minute := at.Hour()*60 + at.Minute()
	if start < end {
		return minute >= start && minute < end
	}
	return minute >= start || minute < end
}
