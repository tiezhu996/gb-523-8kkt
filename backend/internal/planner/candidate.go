package planner

import (
	"errors"
	"sort"

	"datacenter-thermal-capacity-planner/backend/internal/dto"
	"datacenter-thermal-capacity-planner/backend/internal/model"
)

const AlgorithmVersion = "thermal-v1"

// Sentinel failures for read-only relocation trials. The service layer maps
// these to client-facing HTTP errors; they never mutate the evaluated scenario.
var (
	ErrTrialLoadNotInScenario = errors.New("relocation trial load is not part of the scenario snapshot")
	ErrTrialRackNotInScenario = errors.New("relocation trial rack is not part of the scenario snapshot")
	ErrTrialZoneMissing       = errors.New("relocation trial target rack has no zone in the scenario snapshot")
)

type Engine struct {
	maxIterations int
}

type Result struct {
	Assignments []dto.RackAssignment
	ZoneResults []dto.ZoneThermalResult
	Violations  []dto.ConstraintViolation
	TotalPower  float64
	PeakTemp    float64
	Score       float64
}

type rackUsage struct {
	powerKW    float64
	heatKW     float64
	airflowCFM float64
	rackUnits  int
	groups     map[string]bool
}

type candidate struct {
	rack        model.Rack
	zone        model.ThermalZone
	score       float64
	explanation []string
}

func NewEngine(maxIterations int) *Engine {
	if maxIterations < 1 {
		maxIterations = 1
	}
	return &Engine{maxIterations: maxIterations}
}

func (e *Engine) Evaluate(zones []model.ThermalZone, racks []model.Rack, loads []model.EquipmentLoad) Result {
	zoneByID := make(map[uint]model.ThermalZone, len(zones))
	for _, zone := range zones {
		zoneByID[zone.ID] = zone
	}

	orderedRacks := append([]model.Rack(nil), racks...)
	sort.SliceStable(orderedRacks, func(i, j int) bool {
		if orderedRacks[i].RackCode == orderedRacks[j].RackCode {
			return orderedRacks[i].ID < orderedRacks[j].ID
		}
		return orderedRacks[i].RackCode < orderedRacks[j].RackCode
	})
	orderedLoads := append([]model.EquipmentLoad(nil), loads...)
	sort.SliceStable(orderedLoads, func(i, j int) bool {
		left := tightness(orderedLoads[i], orderedRacks)
		right := tightness(orderedLoads[j], orderedRacks)
		if left == right {
			if orderedLoads[i].PowerKW == orderedLoads[j].PowerKW {
				return orderedLoads[i].ID < orderedLoads[j].ID
			}
			return orderedLoads[i].PowerKW > orderedLoads[j].PowerKW
		}
		return left > right
	})

	usage := make(map[uint]*rackUsage, len(orderedRacks))
	zoneHeat := make(map[uint]float64, len(zones))
	zonePower := make(map[uint]float64, len(zones))
	zoneGroups := make(map[uint]map[string]bool, len(zones))
	for _, zone := range zones {
		zoneGroups[zone.ID] = map[string]bool{}
	}
	for _, rack := range orderedRacks {
		usage[rack.ID] = &rackUsage{groups: map[string]bool{}}
	}

	result := Result{Assignments: []dto.RackAssignment{}, ZoneResults: []dto.ZoneThermalResult{}, Violations: []dto.ConstraintViolation{}}
	iterations := 0
	for _, load := range orderedLoads {
		if !load.IsPlannable() {
			result.Violations = append(result.Violations, dto.ConstraintViolation{
				Code: "LOAD_NOT_READY", Severity: "critical", EntityType: "equipment_load", EntityID: load.ID,
				Message: "load is not in ready state and cannot be placed",
			})
			continue
		}
		candidates := make([]candidate, 0, len(orderedRacks))
		var evidence []dto.ConstraintViolation
		for _, rack := range orderedRacks {
			iterations++
			if iterations > e.maxIterations {
				evidence = append(evidence, dto.ConstraintViolation{
					Code: "ITERATION_LIMIT", Severity: "critical", EntityType: "equipment_load", EntityID: load.ID,
					Message: "candidate search reached the configured iteration limit",
				})
				break
			}
			zone, exists := zoneByID[rack.ZoneID]
			if !exists {
				continue
			}
			violations := checkCandidate(load, rack, zone, usage[rack.ID], zoneHeat[rack.ZoneID], zoneGroups[rack.ZoneID])
			if len(violations) > 0 {
				evidence = append(evidence, violations...)
				continue
			}
			score, explanation := placementScore(load, rack, zone, usage[rack.ID], zoneHeat[rack.ZoneID], zones, zoneHeat)
			candidates = append(candidates, candidate{rack: rack, zone: zone, score: score, explanation: explanation})
		}
		if len(candidates) == 0 {
			result.Violations = append(result.Violations, summarizeUnplaced(load, evidence)...)
			continue
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].score == candidates[j].score {
				return candidates[i].rack.RackCode < candidates[j].rack.RackCode
			}
			return candidates[i].score > candidates[j].score
		})
		selected := candidates[0]
		u := usage[selected.rack.ID]
		u.powerKW += load.PowerKW
		u.heatKW += load.HeatKW
		u.airflowCFM += load.AirflowCFM
		u.rackUnits += load.RackUnits
		u.groups[load.RedundancyGroup] = true
		zoneGroups[selected.zone.ID][load.RedundancyGroup] = true
		zoneHeat[selected.zone.ID] += load.HeatKW
		zonePower[selected.zone.ID] += load.PowerKW
		result.TotalPower += load.PowerKW
		result.Assignments = append(result.Assignments, dto.RackAssignment{
			LoadID: load.ID, LoadName: load.Name, RackID: selected.rack.ID, RackCode: selected.rack.RackCode,
			ZoneID: selected.zone.ID, ZoneCode: selected.zone.ZoneCode, PowerKW: load.PowerKW,
			HeatKW: load.HeatKW, AirflowCFM: load.AirflowCFM, RackUnits: load.RackUnits,
			PlacementScore: selected.score, Explanation: selected.explanation,
		})
	}

	thermalResults, thermalViolations, peak := propagateThermal(zones, zoneHeat)
	result.ZoneResults = thermalResults
	result.Violations = append(result.Violations, thermalViolations...)
	result.PeakTemp = peak
	result.Violations = append(result.Violations, validateFinalAssignments(orderedRacks, usage, zones, zonePower, result.Assignments)...)
	result.Score = scenarioScore(result.Assignments, result.ZoneResults, result.Violations)
	return result
}

// RelocationTrial answers a read-only what-if: remove one load from the
// scenario's evaluated placement (freeing its original rack/zone first so it
// cannot block itself), then project the target rack and target thermal zone
// as if the load were placed there. The scenario is never modified and no
// other load moves. All blocking constraints are reported together.
func (e *Engine) RelocationTrial(zones []model.ThermalZone, racks []model.Rack, loads []model.EquipmentLoad, assignments []dto.RackAssignment, loadID, targetRackID uint) (dto.RelocationTrialResponse, error) {
	zoneByID := make(map[uint]model.ThermalZone, len(zones))
	for _, zone := range zones {
		zoneByID[zone.ID] = zone
	}
	rackByID := make(map[uint]model.Rack, len(racks))
	for _, rack := range racks {
		rackByID[rack.ID] = rack
	}
	loadByID := make(map[uint]model.EquipmentLoad, len(loads))
	for _, load := range loads {
		loadByID[load.ID] = load
	}
	mover, exists := loadByID[loadID]
	if !exists {
		return dto.RelocationTrialResponse{}, ErrTrialLoadNotInScenario
	}
	target, exists := rackByID[targetRackID]
	if !exists {
		return dto.RelocationTrialResponse{}, ErrTrialRackNotInScenario
	}
	targetZone, exists := zoneByID[target.ZoneID]
	if !exists {
		return dto.RelocationTrialResponse{}, ErrTrialZoneMissing
	}

	// Rebuild the evaluated rack usage and zone heat, skipping the mover so its
	// original slot is freed before the target is tested.
	usage := make(map[uint]*rackUsage, len(racks))
	for _, rack := range racks {
		usage[rack.ID] = &rackUsage{groups: map[string]bool{}}
	}
	zoneGroups := make(map[uint]map[string]bool, len(zones))
	for _, zone := range zones {
		zoneGroups[zone.ID] = map[string]bool{}
	}
	zoneHeat := make(map[uint]float64, len(zones))
	var moverAssignment *dto.RackAssignment
	for i := range assignments {
		assignment := assignments[i]
		rack, ok := rackByID[assignment.RackID]
		if !ok {
			continue
		}
		if assignment.LoadID == loadID {
			moverAssignment = &assignments[i]
			continue
		}
		u := usage[rack.ID]
		u.powerKW += assignment.PowerKW
		u.heatKW += assignment.HeatKW
		u.airflowCFM += assignment.AirflowCFM
		u.rackUnits += assignment.RackUnits
		load, known := loadByID[assignment.LoadID]
		if known {
			u.groups[load.RedundancyGroup] = true
			zoneGroups[rack.ZoneID][load.RedundancyGroup] = true
		}
		zoneHeat[rack.ZoneID] += assignment.HeatKW
	}

	source := dto.RelocationSource{Remaining: dto.RelocationUsage{}}
	if moverAssignment != nil {
		source.HasPlacement = true
		source.RackID = moverAssignment.RackID
		source.RackCode = moverAssignment.RackCode
		source.ZoneID = moverAssignment.ZoneID
		source.ZoneCode = moverAssignment.ZoneCode
		source.SameRack = moverAssignment.RackID == targetRackID
		if sourceRack, ok := rackByID[moverAssignment.RackID]; ok {
			sourceRemaining := usage[sourceRack.ID]
			source.Remaining = dto.RelocationUsage{
				PowerKW: round2(sourceRemaining.powerKW), AirflowCFM: round2(sourceRemaining.airflowCFM),
				RackUnits: sourceRemaining.rackUnits,
			}
		}
	}

	// Vacated state of the target rack, then the projected state with the mover
	// placed into it.
	targetUsage := usage[targetRackID]
	vacated := dto.RelocationUsage{
		PowerKW: round2(targetUsage.powerKW), AirflowCFM: round2(targetUsage.airflowCFM),
		RackUnits: targetUsage.rackUnits,
	}
	projected := dto.RelocationPlacement{
		PowerKW:    round2(targetUsage.powerKW + mover.PowerKW),
		HeatKW:     round2(targetUsage.heatKW + mover.HeatKW),
		AirflowCFM: round2(targetUsage.airflowCFM + mover.AirflowCFM),
		RackUnits:  targetUsage.rackUnits + mover.RackUnits,
	}
	zoneHeatAfterVacate := zoneHeat[target.ZoneID]
	projectedZoneHeat := zoneHeatAfterVacate + mover.HeatKW

	violations := checkRelocation(mover, target, targetZone, targetUsage, zoneHeatAfterVacate, zoneGroups[target.ZoneID])

	// Re-run the same simplified thermal propagation on the post-relocation heat
	// distribution and keep only what the target zone is responsible for.
	projectedHeat := make(map[uint]float64, len(zoneHeat))
	for id, heat := range zoneHeat {
		projectedHeat[id] = heat
	}
	projectedHeat[target.ZoneID] = projectedZoneHeat
	zoneResults, thermalViolations, _ := propagateThermal(zones, projectedHeat)
	var targetZoneResult dto.ZoneThermalResult
	for _, result := range zoneResults {
		if result.ZoneID == target.ZoneID {
			targetZoneResult = result
			break
		}
	}
	for _, item := range thermalViolations {
		if item.EntityType == "thermal_zone" && item.EntityID == target.ZoneID {
			violations = append(violations, item)
		}
	}

	blocking := map[string]bool{}
	for _, item := range violations {
		if item.Severity == "critical" {
			blocking[item.Code] = true
		}
	}
	codes := make([]string, 0, len(blocking))
	for code := range blocking {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	// Occupied-before is the target rack exactly as the evaluated scenario left
	// it: when the mover already stands in the target rack it counts here, then
	// disappears in Vacated and re-appears in Projected (in-place no-op).
	occupiedBefore := dto.RelocationUsage{
		PowerKW: round2(targetUsage.powerKW), AirflowCFM: round2(targetUsage.airflowCFM),
		RackUnits: targetUsage.rackUnits,
	}
	if moverAssignment != nil && moverAssignment.RackID == targetRackID {
		occupiedBefore = dto.RelocationUsage{
			PowerKW:    round2(targetUsage.powerKW + mover.PowerKW),
			AirflowCFM: round2(targetUsage.airflowCFM + mover.AirflowCFM),
			RackUnits:  targetUsage.rackUnits + mover.RackUnits,
		}
	}

	return dto.RelocationTrialResponse{
		ScenarioID:       0,
		AlgorithmVersion: AlgorithmVersion,
		Load: dto.RelocationLoad{
			ID: mover.ID, Name: mover.Name, PowerKW: mover.PowerKW, HeatKW: mover.HeatKW,
			AirflowCFM: mover.AirflowCFM, RackUnits: mover.RackUnits, RedundancyGroup: mover.RedundancyGroup,
		},
		Source: source,
		TargetRack: dto.RelocationRackTrial{
			RackID: target.ID, RackCode: target.RackCode, ZoneID: target.ZoneID,
			ZoneCode: targetZone.ZoneCode, RackStatus: string(target.RackStatus),
			PowerLimitKW: target.PowerLimitKW, AirflowLimitCFM: target.AirflowLimitCFM,
			RackUnitsLimit: target.RackUnits, OccupiedBefore: occupiedBefore,
			Vacated: vacated, Projected: projected,
			PowerHeadroomKW: round2(target.PowerLimitKW - projected.PowerKW),
			AirflowHeadroom: round2(target.AirflowLimitCFM - projected.AirflowCFM),
			UnitsHeadroom:   target.RackUnits - projected.RackUnits,
		},
		TargetZone: dto.RelocationZoneTrial{
			ZoneID: targetZone.ID, ZoneCode: targetZone.ZoneCode, ZoneStatus: targetZone.ZoneStatus,
			CoolingCapacityKW:        targetZone.CoolingCapacityKW,
			AssignedHeatBefore:       round2(zoneHeatBeforeTarget(zoneHeat, target.ZoneID, moverAssignment, mover.HeatKW)),
			RemainingHeatAfterVacate: round2(zoneHeatAfterVacate),
			ProjectedHeatKW:          targetZoneResult.AssignedHeatKW,
			CoolingMarginKW:          targetZoneResult.CoolingMarginKW,
			SupplyTempC:              targetZone.SupplyTempC, EstimatedReturnC: targetZoneResult.EstimatedReturnC,
			MaxReturnTempC: targetZone.MaxReturnTempC, TemperatureMarginC: targetZoneResult.TemperatureMarginC,
		},
		Violations: violations, Feasible: len(codes) == 0, BlockingConstraints: codes,
	}, nil
}

func zoneHeatBeforeTarget(zoneHeat map[uint]float64, zoneID uint, moverAssignment *dto.RackAssignment, moverHeat float64) float64 {
	before := zoneHeat[zoneID]
	if moverAssignment != nil && moverAssignment.ZoneID == zoneID {
		before += moverHeat
	}
	return before
}

// checkRelocation evaluates every rack/zone placement constraint for the
// projected placement rather than stopping at the first failure, so reviewers
// see everything that blocks a move.
func checkRelocation(load model.EquipmentLoad, rack model.Rack, zone model.ThermalZone, usage *rackUsage, zoneHeat float64, zoneGroups map[string]bool) []dto.ConstraintViolation {
	violations := []dto.ConstraintViolation{}
	if !load.IsPlannable() {
		violations = append(violations, dto.ConstraintViolation{
			Code: "LOAD_NOT_READY", Severity: "critical", EntityType: "equipment_load", EntityID: load.ID,
			Message: "load is not in ready state and cannot be placed",
		})
	}
	if !rack.IsUsable() {
		violations = append(violations, violation("RACK_UNAVAILABLE", rack.ID, "rack", "rack status does not allow placement", 1, 0))
	}
	if !zone.IsActive() {
		violations = append(violations, violation("ZONE_UNAVAILABLE", zone.ID, "thermal_zone", "thermal zone is not active", 1, 0))
	}
	if usage.powerKW+load.PowerKW > rack.PowerLimitKW {
		violations = append(violations, violation("RACK_POWER_LIMIT", rack.ID, "rack", "relocation exceeds rack power limit", usage.powerKW+load.PowerKW, rack.PowerLimitKW))
	}
	if usage.airflowCFM+load.AirflowCFM > rack.AirflowLimitCFM {
		violations = append(violations, violation("RACK_AIRFLOW_LIMIT", rack.ID, "rack", "relocation exceeds rack airflow limit", usage.airflowCFM+load.AirflowCFM, rack.AirflowLimitCFM))
	}
	if usage.rackUnits+load.RackUnits > rack.RackUnits {
		violations = append(violations, violation("RACK_UNIT_LIMIT", rack.ID, "rack", "relocation exceeds available rack units", float64(usage.rackUnits+load.RackUnits), float64(rack.RackUnits)))
	}
	if zoneHeat+load.HeatKW > zone.CoolingCapacityKW {
		violations = append(violations, violation("ZONE_COOLING_LIMIT", zone.ID, "thermal_zone", "relocation exceeds zone cooling capacity", zoneHeat+load.HeatKW, zone.CoolingCapacityKW))
	}
	if zoneGroups[load.RedundancyGroup] {
		violations = append(violations, violation("REDUNDANCY_ZONE_COLLISION", load.ID, "equipment_load", "redundancy peers must be isolated across thermal zones", 1, 0))
	}
	return violations
}

func tightness(load model.EquipmentLoad, racks []model.Rack) float64 {
	best := 0.0
	for _, rack := range racks {
		if !rack.IsUsable() {
			continue
		}
		value := maxFloat(load.PowerKW/rack.PowerLimitKW, load.AirflowCFM/rack.AirflowLimitCFM, float64(load.RackUnits)/float64(rack.RackUnits))
		if value > best {
			best = value
		}
	}
	return best
}
