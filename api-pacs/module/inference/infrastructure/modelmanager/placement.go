package modelmanager

import "sort"

const (
	PlacementExhaustive        = "exhaustive"
	PlacementFirstFit          = "first-fit-decreasing"
	defaultMaxExhaustiveModels = 16
	// maxPlacementCombinations bounds the search when there are many GPUs.
	maxPlacementCombinations = 1 << 20
)

// PlacementGPU is one device a plan may use. CapacityMiB is the schedulable
// budget left for the planned models.
type PlacementGPU struct {
	UUID        string
	CapacityMiB int
	// Allowed devices may receive a model; a model already on a device that is
	// not allowed may stay there.
	Allowed bool
}

// PlacementModel is one model pinned to a GPU. ReservationMiB is what the
// manager reserves while it is loaded.
type PlacementModel struct {
	ID             string
	GPUUUID        string
	ReservationMiB int
	Movable        bool
}

type PlacementMove struct {
	ModelID        string
	From, To       string
	ReservationMiB int
}

type PlacementGPUResult struct {
	UUID                                string
	CapacityMiB                         int
	ReservedBeforeMiB, ReservedAfterMiB int
	ModelsBefore, ModelsAfter           int
	FitBefore, FitAfter                 int
}

// PlacementPlan is the result of PlanPlacement. FitBefore and FitAfter count
// the models whose reservations can be held at the same time.
type PlacementPlan struct {
	Strategy            string
	Current, Proposed   map[string]string
	Moves               []PlacementMove
	GPUs                []PlacementGPUResult
	Models              int
	FitBefore, FitAfter int
	// Improves is true only when more models fit after the moves than before.
	Improves bool
}

type placementScore struct {
	fit, moves, minFree int
}

func (a placementScore) better(b placementScore) bool {
	if a.fit != b.fit {
		return a.fit > b.fit
	}
	if a.moves != b.moves {
		return a.moves < b.moves
	}
	return a.minFree > b.minFree
}

// PlanPlacement proposes a GPU for every model. It is a pure function: it
// maximises the number of models whose reservations fit at the same time, then
// minimises the number of moves, then keeps the tightest device as free as
// possible. A model that is not movable, or that would not fit alone on a
// device, is never sent there. Models on a GPU that is not listed are ignored.
// Up to maxExhaustive movable models every assignment is examined; above it a
// first-fit-decreasing pass is used. maxExhaustive <= 0 selects the default.
func PlanPlacement(gpus []PlacementGPU, models []PlacementModel, maxExhaustive int) PlacementPlan {
	if maxExhaustive <= 0 {
		maxExhaustive = defaultMaxExhaustiveModels
	}
	gpus = append([]PlacementGPU(nil), gpus...)
	sort.Slice(gpus, func(i, j int) bool { return gpus[i].UUID < gpus[j].UUID })
	index := make(map[string]int, len(gpus))
	for i, gpu := range gpus {
		index[gpu.UUID] = i
	}
	planned := make([]PlacementModel, 0, len(models))
	for _, model := range models {
		if _, ok := index[model.GPUUUID]; ok && model.ID != "" {
			planned = append(planned, model)
		}
	}
	sort.Slice(planned, func(i, j int) bool { return planned[i].ID < planned[j].ID })

	current := make([]int, len(planned))
	// options[i] lists the devices model i may be on, its current one first so
	// that ties keep a model where it is.
	options := make([][]int, len(planned))
	combinations, movable := 1, 0
	for i, model := range planned {
		current[i] = index[model.GPUUUID]
		options[i] = []int{current[i]}
		if !model.Movable {
			continue
		}
		for g, gpu := range gpus {
			if g != current[i] && gpu.Allowed && model.ReservationMiB <= gpu.CapacityMiB {
				options[i] = append(options[i], g)
			}
		}
		if len(options[i]) > 1 {
			movable++
			if combinations <= maxPlacementCombinations {
				combinations *= len(options[i])
			}
		}
	}

	score := func(assignment []int) (placementScore, []int) {
		perGPU := make([][]int, len(gpus))
		result := placementScore{}
		for i, g := range assignment {
			perGPU[g] = append(perGPU[g], planned[i].ReservationMiB)
			if g != current[i] {
				result.moves++
			}
		}
		fits := make([]int, len(gpus))
		for g, reservations := range perGPU {
			// Admitting the smallest reservations first maximises how many fit.
			sort.Ints(reservations)
			used, total := 0, 0
			for _, reservation := range reservations {
				total += reservation
				if used+reservation <= gpus[g].CapacityMiB {
					used += reservation
					fits[g]++
				}
			}
			result.fit += fits[g]
			if free := gpus[g].CapacityMiB - total; g == 0 || free < result.minFree {
				result.minFree = free
			}
		}
		return result, fits
	}

	best := append([]int(nil), current...)
	bestScore, _ := score(best)
	strategy := PlacementExhaustive
	if movable > maxExhaustive || combinations > maxPlacementCombinations {
		strategy = PlacementFirstFit
		candidate := firstFitDecreasing(gpus, planned, current, options)
		if candidateScore, _ := score(candidate); candidateScore.better(bestScore) {
			best, bestScore = candidate, candidateScore
		}
	} else if len(planned) > 0 {
		choice := make([]int, len(planned))
		assignment := append([]int(nil), current...)
		for {
			// Advance the odometer; the all-zero choice is the current assignment.
			position := 0
			for position < len(choice) {
				choice[position]++
				if choice[position] < len(options[position]) {
					break
				}
				choice[position] = 0
				position++
			}
			if position == len(choice) {
				break
			}
			for i := 0; i <= position; i++ {
				assignment[i] = options[i][choice[i]]
			}
			if candidateScore, _ := score(assignment); candidateScore.better(bestScore) {
				bestScore = candidateScore
				copy(best, assignment)
			}
		}
	}

	plan := PlacementPlan{Strategy: strategy, Current: map[string]string{}, Proposed: map[string]string{}, Moves: []PlacementMove{}, Models: len(planned)}
	beforeScore, fitBefore := score(current)
	_, fitAfter := score(best)
	plan.FitBefore, plan.FitAfter = beforeScore.fit, bestScore.fit
	plan.Improves = plan.FitAfter > plan.FitBefore
	results := make([]PlacementGPUResult, len(gpus))
	for g, gpu := range gpus {
		results[g] = PlacementGPUResult{UUID: gpu.UUID, CapacityMiB: gpu.CapacityMiB, FitBefore: fitBefore[g], FitAfter: fitAfter[g]}
	}
	for i, model := range planned {
		plan.Current[model.ID] = gpus[current[i]].UUID
		plan.Proposed[model.ID] = gpus[best[i]].UUID
		results[current[i]].ReservedBeforeMiB += model.ReservationMiB
		results[current[i]].ModelsBefore++
		results[best[i]].ReservedAfterMiB += model.ReservationMiB
		results[best[i]].ModelsAfter++
		if best[i] != current[i] {
			plan.Moves = append(plan.Moves, PlacementMove{ModelID: model.ID, From: gpus[current[i]].UUID, To: gpus[best[i]].UUID, ReservationMiB: model.ReservationMiB})
		}
	}
	plan.GPUs = results
	orderMoves(&plan)
	return plan
}

// firstFitDecreasing keeps every model that fits where it is and sends the
// others, largest first, to the allowed device with the most free budget.
func firstFitDecreasing(gpus []PlacementGPU, models []PlacementModel, current []int, options [][]int) []int {
	assignment := append([]int(nil), current...)
	free := make([]int, len(gpus))
	for g, gpu := range gpus {
		free[g] = gpu.CapacityMiB
	}
	order := make([]int, 0, len(models))
	for i := range models {
		if len(options[i]) == 1 {
			free[current[i]] -= models[i].ReservationMiB
		} else {
			order = append(order, i)
		}
	}
	sort.SliceStable(order, func(a, b int) bool {
		return models[order[a]].ReservationMiB > models[order[b]].ReservationMiB
	})
	for _, i := range order {
		target := current[i]
		if models[i].ReservationMiB > free[target] {
			roomiest := -1
			for _, g := range options[i][1:] {
				if models[i].ReservationMiB <= free[g] && (roomiest < 0 || free[g] > free[roomiest]) {
					roomiest = g
				}
			}
			if roomiest >= 0 {
				target = roomiest
			}
		}
		assignment[i] = target
		free[target] -= models[i].ReservationMiB
	}
	return assignment
}

// orderMoves puts first the moves whose destination already has room, so that
// applying them one at a time never needs a device to be over its budget
// longer than necessary. Larger models go first within each group.
func orderMoves(plan *PlacementPlan) {
	free := map[string]int{}
	for _, gpu := range plan.GPUs {
		free[gpu.UUID] = gpu.CapacityMiB - gpu.ReservedBeforeMiB
	}
	remaining := append([]PlacementMove(nil), plan.Moves...)
	sort.SliceStable(remaining, func(i, j int) bool { return remaining[i].ReservationMiB > remaining[j].ReservationMiB })
	ordered := make([]PlacementMove, 0, len(remaining))
	for len(remaining) > 0 {
		pick := 0
		for i, move := range remaining {
			if move.ReservationMiB <= free[move.To] {
				pick = i
				break
			}
		}
		move := remaining[pick]
		free[move.To] -= move.ReservationMiB
		free[move.From] += move.ReservationMiB
		ordered = append(ordered, move)
		remaining = append(remaining[:pick], remaining[pick+1:]...)
	}
	plan.Moves = ordered
}
