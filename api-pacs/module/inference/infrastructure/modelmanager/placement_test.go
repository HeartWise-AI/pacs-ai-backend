package modelmanager

import (
	"fmt"
	"reflect"
	"testing"
)

func planGPUs(capacityA, capacityB int) []PlacementGPU {
	return []PlacementGPU{{UUID: gpuA, CapacityMiB: capacityA, Allowed: true}, {UUID: gpuB, CapacityMiB: capacityB, Allowed: true}}
}

func planModel(name, gpu string, reservation int) PlacementModel {
	return PlacementModel{ID: name, GPUUUID: gpu, ReservationMiB: reservation, Movable: true}
}

func moved(plan PlacementPlan) []string {
	names := []string{}
	for _, move := range plan.Moves {
		names = append(names, move.ModelID+">"+move.To)
	}
	return names
}

func TestPlanMovesTheFewestModelsUntilEverythingFits(t *testing.T) {
	// The production picture: seven models on the 3090, three on the A5000,
	// 20480 MiB schedulable on each.
	models := []PlacementModel{
		planModel("cto", gpuB, 5200), planModel("mace", gpuB, 5200), planModel("syntax", gpuB, 5200), planModel("generic", gpuB, 5200),
		planModel("cathef", gpuB, 5200), planModel("deeprv", gpuB, 5200), planModel("scad", gpuB, 5200),
		planModel("jepa", gpuA, 9000), planModel("echo", gpuA, 3000), planModel("classifier", gpuA, 1500),
	}
	plan := PlanPlacement(planGPUs(20480, 20480), models, 0)
	if plan.Strategy != PlacementExhaustive || plan.Models != 10 || plan.FitBefore != 6 || !plan.Improves {
		t.Fatalf("unexpected starting point: %+v", plan)
	}
	// The count is what is maximised: three 5200 MiB models move next to the
	// small ones, where five then fit at once, instead of leaving them queued
	// behind each other on the full device.
	if plan.FitAfter != 8 || len(plan.Moves) != 3 {
		t.Fatalf("expected three moves from the full GPU: fit=%d moves=%+v", plan.FitAfter, plan.Moves)
	}
	for _, move := range plan.Moves {
		if move.From != gpuB || move.To != gpuA || move.ReservationMiB != 5200 || plan.Current[move.ModelID] != gpuB || plan.Proposed[move.ModelID] != gpuA {
			t.Fatalf("unexpected move %+v", move)
		}
	}
	if plan.Proposed["jepa"] != gpuA || plan.Proposed["scad"] != gpuB {
		t.Fatalf("assignments do not match the moves: %+v", plan.Proposed)
	}
	for _, gpu := range plan.GPUs {
		want := map[string][6]int{gpuA: {13500, 29100, 3, 6, 3, 5}, gpuB: {36400, 20800, 7, 4, 3, 3}}[gpu.UUID]
		if got := [6]int{gpu.ReservedBeforeMiB, gpu.ReservedAfterMiB, gpu.ModelsBefore, gpu.ModelsAfter, gpu.FitBefore, gpu.FitAfter}; got != want {
			t.Fatalf("%s totals %v, want %v", gpu.UUID, got, want)
		}
	}

	// With measured reservations all ten fit, again with the fewest moves.
	for i := range models[:7] {
		models[i].ReservationMiB = 3200
	}
	plan = PlanPlacement(planGPUs(20480, 20480), models, 0)
	if plan.FitBefore != 9 || plan.FitAfter != 10 || len(plan.Moves) != 1 || plan.Moves[0].To != gpuA {
		t.Fatalf("measured plan: fit %d -> %d moves=%+v", plan.FitBefore, plan.FitAfter, plan.Moves)
	}
}

func TestPlanLeavesAPlacementThatAlreadyFitsAlone(t *testing.T) {
	// Everything fits although one GPU is nearly full and the other empty:
	// balance alone never justifies redeploying a model.
	plan := PlanPlacement(planGPUs(100, 100), []PlacementModel{planModel("a", gpuA, 50), planModel("b", gpuA, 45)}, 0)
	if len(plan.Moves) != 0 || plan.Improves || plan.FitBefore != 2 || plan.FitAfter != 2 || !reflect.DeepEqual(plan.Current, plan.Proposed) {
		t.Fatalf("unexpected moves: %+v", plan)
	}
	if empty := PlanPlacement(planGPUs(100, 100), nil, 0); len(empty.Moves) != 0 || empty.Improves || len(empty.GPUs) != 2 {
		t.Fatalf("empty plan: %+v", empty)
	}
}

func TestPlanNeverMovesImmovableModelsOrTargetsDisallowedGPUs(t *testing.T) {
	big := PlacementModel{ID: "pinned-big", GPUUUID: gpuA, ReservationMiB: 70}
	models := []PlacementModel{big, planModel("small", gpuA, 40), planModel("tiny", gpuA, 20)}
	plan := PlanPlacement(planGPUs(100, 100), models, 0)
	// Moving the immovable 70 would also work; the plan must move "small".
	if got := moved(plan); !reflect.DeepEqual(got, []string{"small>" + gpuB}) || plan.FitAfter != 3 {
		t.Fatalf("moves=%v fit=%d", got, plan.FitAfter)
	}
	// Nothing may be sent to a GPU outside the allowed list.
	gpus := planGPUs(100, 100)
	gpus[1].Allowed = false
	if plan = PlanPlacement(gpus, models, 0); len(plan.Moves) != 0 || plan.FitAfter != 2 || plan.Improves {
		t.Fatalf("a disallowed GPU received a model: %+v", plan.Moves)
	}
	// A model already on a disallowed GPU may stay or leave it.
	gpus = planGPUs(100, 100)
	gpus[0].Allowed = false
	if plan = PlanPlacement(gpus, models, 0); !reflect.DeepEqual(moved(plan), []string{"small>" + gpuB}) {
		t.Fatalf("moves=%v", moved(plan))
	}
	// A model larger than the other device is never sent there.
	plan = PlanPlacement(planGPUs(100, 30), []PlacementModel{planModel("x", gpuA, 60), planModel("y", gpuA, 50)}, 0)
	if len(plan.Moves) != 0 || plan.FitAfter != 1 {
		t.Fatalf("an oversized model was moved: %+v", plan.Moves)
	}
	// Models on a GPU that is not planned are ignored, not relocated.
	plan = PlanPlacement(planGPUs(100, 100), []PlacementModel{planModel("stray", "GPU-unknown", 10), planModel("a", gpuA, 10)}, 0)
	if _, present := plan.Proposed["stray"]; present || plan.Models != 1 {
		t.Fatalf("unplaced model was planned: %+v", plan)
	}
}

func TestPlanIsBestEffortWhenNotEverythingCanFit(t *testing.T) {
	models := []PlacementModel{planModel("a", gpuA, 60), planModel("b", gpuA, 60), planModel("c", gpuA, 60), planModel("d", gpuA, 30)}
	plan := PlanPlacement(planGPUs(100, 100), models, 0)
	// 210 MiB in 200: at most three models fit (60+30 and 60); a single move does it.
	if plan.FitBefore != 2 || plan.FitAfter != 3 || len(plan.Moves) != 1 || plan.Moves[0].ReservationMiB != 60 {
		t.Fatalf("best effort: fit %d -> %d moves=%+v", plan.FitBefore, plan.FitAfter, plan.Moves)
	}
}

func TestPlanBreaksTiesByFreeBudgetAndIsDeterministic(t *testing.T) {
	// Either model could move; moving the larger leaves 40 and 60 MiB free,
	// moving the smaller would leave only 10 MiB on GPU A... both are one move.
	models := []PlacementModel{planModel("big", gpuA, 60), planModel("mid", gpuA, 50), planModel("low", gpuB, 40)}
	plan := PlanPlacement(planGPUs(100, 110), models, 0)
	// big>B leaves A 50 free, B 10 free; mid>B leaves A 40 free, B 20 free.
	if got := moved(plan); !reflect.DeepEqual(got, []string{"mid>" + gpuB}) {
		t.Fatalf("tie-break by the tightest device failed: %v", got)
	}
	shuffled := []PlacementModel{models[2], models[0], models[1]}
	gpus := planGPUs(100, 110)
	gpus[0], gpus[1] = gpus[1], gpus[0]
	if again := PlanPlacement(gpus, shuffled, 0); !reflect.DeepEqual(again, plan) {
		t.Fatalf("the plan depends on input order:\n%+v\n%+v", again, plan)
	}
}

func TestPlanFindsASwapAndOrdersMovesByAvailableRoom(t *testing.T) {
	// The large model on A only fits on B once B gives up its medium model.
	models := []PlacementModel{planModel("large", gpuA, 80), {ID: "fixed", GPUUUID: gpuA, ReservationMiB: 60}, planModel("medium", gpuB, 40), planModel("other", gpuB, 30)}
	plan := PlanPlacement(planGPUs(100, 110), models, 0)
	if plan.FitBefore != 3 || plan.FitAfter != 4 {
		t.Fatalf("fit %d -> %d: %+v", plan.FitBefore, plan.FitAfter, plan.Moves)
	}
	// Neither destination has room yet, so the larger model leaves the
	// overfull device first.
	if got := moved(plan); !reflect.DeepEqual(got, []string{"large>" + gpuB, "medium>" + gpuA}) {
		t.Fatalf("swap moves: %v", got)
	}

	// When one destination has room and another does not, the move that fits
	// goes first even though it is the smaller one.
	const gpuC = "GPU-33333333-3333-3333-3333-333333333333"
	chained := PlacementPlan{
		GPUs:  []PlacementGPUResult{{UUID: gpuA, CapacityMiB: 100, ReservedBeforeMiB: 120}, {UUID: gpuB, CapacityMiB: 100, ReservedBeforeMiB: 50}, {UUID: gpuC, CapacityMiB: 100}},
		Moves: []PlacementMove{{ModelID: "sixty", From: gpuA, To: gpuB, ReservationMiB: 60}, {ModelID: "thirty", From: gpuB, To: gpuC, ReservationMiB: 30}},
	}
	orderMoves(&chained)
	if got := moved(chained); !reflect.DeepEqual(got, []string{"thirty>" + gpuC, "sixty>" + gpuB}) {
		t.Fatalf("moves are not applicable in order: %v", got)
	}
}

func TestPlanFallsBackToFirstFitDecreasingAboveTheLimit(t *testing.T) {
	var models []PlacementModel
	for n := 0; n < 24; n++ {
		models = append(models, planModel(fmt.Sprintf("m%02d", n), gpuA, 10+n%3))
	}
	plan := PlanPlacement(planGPUs(150, 150), models, 0)
	if plan.Strategy != PlacementFirstFit || plan.FitAfter != 24 || !plan.Improves || plan.FitBefore >= 24 {
		t.Fatalf("fallback plan: %+v", plan)
	}
	for _, gpu := range plan.GPUs {
		if gpu.ReservedAfterMiB > gpu.CapacityMiB {
			t.Fatalf("fallback overbooked %s: %+v", gpu.UUID, gpu)
		}
	}
	for _, move := range plan.Moves {
		if move.From != gpuA || move.To != gpuB {
			t.Fatalf("unexpected move %+v", move)
		}
	}
	// The limit is a parameter, and the fallback keeps immovable models and
	// never makes things worse than the current placement.
	small := []PlacementModel{{ID: "fixed", GPUUUID: gpuA, ReservationMiB: 70}, planModel("a", gpuA, 40), planModel("b", gpuA, 20)}
	if plan = PlanPlacement(planGPUs(100, 100), small, 1); plan.Strategy != PlacementFirstFit || plan.Proposed["fixed"] != gpuA || plan.FitAfter != 3 || len(plan.Moves) != 1 {
		t.Fatalf("limited plan: %+v", plan)
	}
	if plan = PlanPlacement(planGPUs(100, 100), []PlacementModel{planModel("a", gpuA, 50), planModel("b", gpuA, 45), planModel("c", gpuB, 10)}, 1); len(plan.Moves) != 0 {
		t.Fatalf("fallback moved a model that already fits: %+v", plan.Moves)
	}
}
