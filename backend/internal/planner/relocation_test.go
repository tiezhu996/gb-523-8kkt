package planner

import (
	"errors"
	"testing"

	"datacenter-thermal-capacity-planner/backend/internal/constants"
	"datacenter-thermal-capacity-planner/backend/internal/dto"
	"datacenter-thermal-capacity-planner/backend/internal/model"
)

func relocationFixture() ([]model.ThermalZone, []model.Rack, []model.EquipmentLoad) {
	zones := []model.ThermalZone{
		{ID: 1, ZoneCode: "A", CoolingCapacityKW: 40, SupplyTempC: 18, MaxReturnTempC: 31, AdjacencyJSON: `{"B":0.2}`, ZoneStatus: "active"},
		{ID: 2, ZoneCode: "B", CoolingCapacityKW: 40, SupplyTempC: 18, MaxReturnTempC: 31, AdjacencyJSON: `{"A":0.2}`, ZoneStatus: "active"},
	}
	racks := []model.Rack{
		{ID: 1, ZoneID: 1, RackCode: "A-01", PowerLimitKW: 24, AirflowLimitCFM: 7000, RackUnits: 42, RackStatus: constants.RackAvailable},
		{ID: 2, ZoneID: 2, RackCode: "B-01", PowerLimitKW: 24, AirflowLimitCFM: 7000, RackUnits: 42, RackStatus: constants.RackAvailable},
	}
	loads := []model.EquipmentLoad{
		{ID: 1, Name: "Compute A", PowerKW: 10, HeatKW: 9.4, AirflowCFM: 2000, RackUnits: 8, RedundancyGroup: "PAIR", LoadStatus: "ready"},
		{ID: 2, Name: "Compute B", PowerKW: 9, HeatKW: 8.5, AirflowCFM: 1800, RackUnits: 8, RedundancyGroup: "PAIR", LoadStatus: "ready"},
		{ID: 3, Name: "Local", PowerKW: 4, HeatKW: 3.6, AirflowCFM: 900, RackUnits: 4, RedundancyGroup: "SOLO", LoadStatus: "ready"},
	}
	return zones, racks, loads
}

func evaluatedAssignments() []dto.RackAssignment {
	return []dto.RackAssignment{
		{LoadID: 1, LoadName: "Compute A", RackID: 1, RackCode: "A-01", ZoneID: 1, ZoneCode: "A", PowerKW: 10, HeatKW: 9.4, AirflowCFM: 2000, RackUnits: 8},
		{LoadID: 2, LoadName: "Compute B", RackID: 2, RackCode: "B-01", ZoneID: 2, ZoneCode: "B", PowerKW: 9, HeatKW: 8.5, AirflowCFM: 1800, RackUnits: 8},
		{LoadID: 3, LoadName: "Local", RackID: 1, RackCode: "A-01", ZoneID: 1, ZoneCode: "A", PowerKW: 4, HeatKW: 3.6, AirflowCFM: 900, RackUnits: 4},
	}
}

func hasViolationCode(response dto.RelocationTrialResponse, code string) bool {
	for _, item := range response.Violations {
		if item.Code == code {
			return true
		}
	}
	return false
}

func TestRelocationTrial(t *testing.T) {
	tests := []struct {
		name         string
		loadID       uint
		targetRackID uint
		mutate       func(zones *[]model.ThermalZone, racks *[]model.Rack, loads *[]model.EquipmentLoad)
		wantFeasible bool
		wantCode     string
		wantPower    float64
		wantUnits    int
	}{
		{
			name: "cross-zone move fits", loadID: 1, targetRackID: 2,
			wantFeasible: false, wantCode: "REDUNDANCY_ZONE_COLLISION",
		},
		{
			name: "solo load moves into rack with peer", loadID: 3, targetRackID: 2,
			wantFeasible: true, wantPower: 13, wantUnits: 12,
		},
		{
			name: "in-place trial frees slot before testing", loadID: 1, targetRackID: 1,
			wantFeasible: true, wantPower: 14, wantUnits: 12,
		},
		{
			name: "rack power limit blocks", loadID: 3, targetRackID: 2,
			mutate: func(zones *[]model.ThermalZone, racks *[]model.Rack, loads *[]model.EquipmentLoad) {
				(*loads)[2].PowerKW = 20
				(*loads)[2].HeatKW = 18
			},
			wantFeasible: false, wantCode: "RACK_POWER_LIMIT",
		},
		{
			name: "rack units block after vacate", loadID: 3, targetRackID: 2,
			mutate: func(zones *[]model.ThermalZone, racks *[]model.Rack, loads *[]model.EquipmentLoad) {
				(*loads)[2].RackUnits = 40
			},
			wantFeasible: false, wantCode: "RACK_UNIT_LIMIT",
		},
		{
			name: "unavailable target rack blocks", loadID: 3, targetRackID: 2,
			mutate: func(zones *[]model.ThermalZone, racks *[]model.Rack, loads *[]model.EquipmentLoad) {
				(*racks)[1].RackStatus = constants.RackMaintenance
			},
			wantFeasible: false, wantCode: "RACK_UNAVAILABLE",
		},
		{
			name: "zone cooling envelope blocks projected heat", loadID: 3, targetRackID: 2,
			mutate: func(zones *[]model.ThermalZone, racks *[]model.Rack, loads *[]model.EquipmentLoad) {
				(*zones)[1].CoolingCapacityKW = 10
			},
			wantFeasible: false, wantCode: "ZONE_COOLING_LIMIT",
		},
		{
			name: "unplaced load can still be trialed", loadID: 4, targetRackID: 2,
			mutate: func(zones *[]model.ThermalZone, racks *[]model.Rack, loads *[]model.EquipmentLoad) {
				*loads = append(*loads, model.EquipmentLoad{ID: 4, Name: "Unplaced", PowerKW: 2, HeatKW: 1.8, AirflowCFM: 400, RackUnits: 2, RedundancyGroup: "LATER", LoadStatus: "ready"})
			},
			wantFeasible: true, wantPower: 11, wantUnits: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			zones, racks, loads := relocationFixture()
			if tt.mutate != nil {
				tt.mutate(&zones, &racks, &loads)
			}
			assignments := evaluatedAssignments()
			response, err := NewEngine(100).RelocationTrial(zones, racks, loads, assignments, tt.loadID, tt.targetRackID)
			if err != nil {
				t.Fatalf("relocation trial failed: %v", err)
			}
			if response.Feasible != tt.wantFeasible {
				t.Fatalf("feasible=%t want %t; violations=%+v", response.Feasible, tt.wantFeasible, response.Violations)
			}
			if tt.wantCode != "" && !hasViolationCode(response, tt.wantCode) {
				t.Fatalf("expected violation %s, got %+v", tt.wantCode, response.Violations)
			}
			if tt.wantFeasible {
				if tt.wantPower != 0 && response.TargetRack.Projected.PowerKW != tt.wantPower {
					t.Fatalf("projected power=%.2f want %.2f", response.TargetRack.Projected.PowerKW, tt.wantPower)
				}
				if tt.wantUnits != 0 && response.TargetRack.Projected.RackUnits != tt.wantUnits {
					t.Fatalf("projected units=%d want %d", response.TargetRack.Projected.RackUnits, tt.wantUnits)
				}
			}
		})
	}
}

func TestRelocationTrialVacatesBeforeTesting(t *testing.T) {
	zones, racks, loads := relocationFixture()
	// Rack A-01 holds Compute A (8U/10kW) + Local (4U/4kW). Moving Local inside
	// the same rack must free Local first: projected equals the original 12U/14kW,
	// not 16U/18kW which would wrongly count Local twice.
	response, err := NewEngine(100).RelocationTrial(zones, racks, loads, evaluatedAssignments(), 3, 1)
	if err != nil {
		t.Fatalf("trial failed: %v", err)
	}
	if !response.Feasible {
		t.Fatalf("in-rack move must be feasible, violations=%+v", response.Violations)
	}
	if response.TargetRack.OccupiedBefore.RackUnits != 12 || response.TargetRack.Vacated.RackUnits != 8 || response.TargetRack.Projected.RackUnits != 12 {
		t.Fatalf("unexpected unit progression: before=%d vacated=%d projected=%d",
			response.TargetRack.OccupiedBefore.RackUnits, response.TargetRack.Vacated.RackUnits, response.TargetRack.Projected.RackUnits)
	}
	if !response.Source.HasPlacement || !response.Source.SameRack {
		t.Fatalf("expected same-rack source, got %+v", response.Source)
	}
	if response.Source.Remaining.PowerKW != 10 {
		t.Fatalf("source remaining power after vacate=%.2f want 10", response.Source.Remaining.PowerKW)
	}
}

func TestRelocationTrialThermalProjection(t *testing.T) {
	zones, racks, loads := relocationFixture()
	response, err := NewEngine(100).RelocationTrial(zones, racks, loads, evaluatedAssignments(), 3, 2)
	if err != nil {
		t.Fatalf("trial failed: %v", err)
	}
	// Zone B goes from 8.5kW to 12.1kW direct heat; A keeps neighbor influence.
	if response.TargetZone.AssignedHeatBefore != 8.5 {
		t.Fatalf("heat before=%.2f want 8.5", response.TargetZone.AssignedHeatBefore)
	}
	if response.TargetZone.ProjectedHeatKW != 12.1 {
		t.Fatalf("projected heat=%.2f want 12.1", response.TargetZone.ProjectedHeatKW)
	}
	if response.TargetZone.RemainingHeatAfterVacate != 8.5 {
		t.Fatalf("remaining heat after vacate=%.2f want 8.5 (mover came from another zone)", response.TargetZone.RemainingHeatAfterVacate)
	}
	if response.TargetZone.EstimatedReturnC <= 18 || response.TargetZone.TemperatureMarginC <= 0 {
		t.Fatalf("unexpected thermal projection: %+v", response.TargetZone)
	}
}

func TestRelocationTrialErrors(t *testing.T) {
	zones, racks, loads := relocationFixture()
	engine := NewEngine(100)
	if _, err := engine.RelocationTrial(zones, racks, loads, evaluatedAssignments(), 999, 1); !errors.Is(err, ErrTrialLoadNotInScenario) {
		t.Fatalf("expected ErrTrialLoadNotInScenario, got %v", err)
	}
	if _, err := engine.RelocationTrial(zones, racks, loads, evaluatedAssignments(), 1, 999); !errors.Is(err, ErrTrialRackNotInScenario) {
		t.Fatalf("expected ErrTrialRackNotInScenario, got %v", err)
	}
}
