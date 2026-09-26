package planner

import (
	"fmt"
	"sort"
	"strings"

	"datacenter-thermal-capacity-planner/backend/internal/dto"
	"datacenter-thermal-capacity-planner/backend/internal/model"
)

// Relocation describes a read-only what-if move of one load into a target rack,
// evaluated against the frozen placement result of an evaluated scenario.
type Relocation struct {
	Zones       []model.ThermalZone
	Loads       []model.EquipmentLoad
	Assignments []dto.RackAssignment
	Load        model.EquipmentLoad
	Rack        model.Rack
	Zone        model.ThermalZone
}

type RelocationOutcome struct {
	SourceRackID uint
	Rack         dto.RelocationRackResult
	Zone         dto.ZoneThermalResult
	Feasible     bool
	Violations   []dto.ConstraintViolation
	Summary      string
}

// SimulateRelocation replays the scenario placement without the moved load, then
// checks the target rack and zone as if the load landed there. Nothing is
// persisted and every other assignment stays untouched.
func SimulateRelocation(input Relocation) RelocationOutcome {
	groupByLoad := make(map[uint]string, len(input.Loads))
	for _, item := range input.Loads {
		groupByLoad[item.ID] = item.RedundancyGroup
	}
	usage := make(map[uint]*rackUsage, len(input.Assignments))
	zoneHeat := map[uint]float64{}
	zoneGroups := map[uint]map[string]bool{}
	sourceRackID := uint(0)
	for _, assignment := range input.Assignments {
		if assignment.LoadID == input.Load.ID {
			// Vacate the current slot first so the load never blocks itself.
			sourceRackID = assignment.RackID
			continue
		}
		u := usage[assignment.RackID]
		if u == nil {
			u = &rackUsage{groups: map[string]bool{}}
			usage[assignment.RackID] = u
		}
		u.powerKW += assignment.PowerKW
		u.heatKW += assignment.HeatKW
		u.airflowCFM += assignment.AirflowCFM
		u.rackUnits += assignment.RackUnits
		zoneHeat[assignment.ZoneID] += assignment.HeatKW
		if zoneGroups[assignment.ZoneID] == nil {
			zoneGroups[assignment.ZoneID] = map[string]bool{}
		}
		zoneGroups[assignment.ZoneID][groupByLoad[assignment.LoadID]] = true
	}

	targetUsage := usage[input.Rack.ID]
	if targetUsage == nil {
		targetUsage = &rackUsage{groups: map[string]bool{}}
	}
	violations := checkCandidate(input.Load, input.Rack, input.Zone, targetUsage, zoneHeat[input.Zone.ID], zoneGroups[input.Zone.ID])

	zoneHeatAfter := make(map[uint]float64, len(zoneHeat)+1)
	for zoneID, heat := range zoneHeat {
		zoneHeatAfter[zoneID] = heat
	}
	zoneHeatAfter[input.Zone.ID] += input.Load.HeatKW
	results, thermalViolations, _ := propagateThermal(input.Zones, zoneHeatAfter)
	zoneResult := dto.ZoneThermalResult{ZoneID: input.Zone.ID, ZoneCode: input.Zone.ZoneCode}
	for _, result := range results {
		if result.ZoneID == input.Zone.ID {
			zoneResult = result
			break
		}
	}
	for _, item := range thermalViolations {
		if item.EntityType == "thermal_zone" && item.EntityID == input.Zone.ID {
			violations = append(violations, item)
		}
	}

	feasible := true
	for _, item := range violations {
		if item.Severity == "critical" {
			feasible = false
			break
		}
	}
	return RelocationOutcome{
		SourceRackID: sourceRackID,
		Rack: dto.RelocationRackResult{
			RackID: input.Rack.ID, RackCode: input.Rack.RackCode,
			PowerKW: round2(targetUsage.powerKW + input.Load.PowerKW), PowerLimitKW: input.Rack.PowerLimitKW,
			AirflowCFM: round2(targetUsage.airflowCFM + input.Load.AirflowCFM), AirflowLimitCFM: input.Rack.AirflowLimitCFM,
			RackUnits: targetUsage.rackUnits + input.Load.RackUnits, RackUnitLimit: input.Rack.RackUnits,
		},
		Zone:       zoneResult,
		Feasible:   feasible,
		Violations: violations,
		Summary:    relocationSummary(feasible, violations),
	}
}

func relocationSummary(feasible bool, violations []dto.ConstraintViolation) string {
	if len(violations) == 0 {
		return "relocation fits: rack power, airflow, rack units, and zone cooling all clear"
	}
	if !feasible {
		codes := map[string]bool{}
		for _, item := range violations {
			if item.Severity == "critical" {
				codes[item.Code] = true
			}
		}
		blocking := make([]string, 0, len(codes))
		for code := range codes {
			blocking = append(blocking, code)
		}
		sort.Strings(blocking)
		return fmt.Sprintf("relocation blocked by %s", strings.Join(blocking, ", "))
	}
	return "relocation fits with warnings; review the constraint evidence"
}
