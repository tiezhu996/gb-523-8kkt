package planner

import (
	"strings"
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
		{ID: 3, ZoneID: 1, RackCode: "A-02", PowerLimitKW: 24, AirflowLimitCFM: 7000, RackUnits: 42, RackStatus: constants.RackAvailable},
		{ID: 4, ZoneID: 2, RackCode: "B-02", PowerLimitKW: 24, AirflowLimitCFM: 7000, RackUnits: 42, RackStatus: constants.RackMaintenance},
	}
	loads := []model.EquipmentLoad{
		{ID: 1, Name: "Compute A", PowerKW: 5, HeatKW: 4.7, AirflowCFM: 1000, RackUnits: 4, RedundancyGroup: "G1", LoadStatus: "ready"},
		{ID: 2, Name: "Compute B", PowerKW: 10, HeatKW: 9.4, AirflowCFM: 2000, RackUnits: 8, RedundancyGroup: "G2", LoadStatus: "ready"},
		{ID: 3, Name: "Compute C", PowerKW: 6, HeatKW: 5.6, AirflowCFM: 1200, RackUnits: 6, RedundancyGroup: "G1", LoadStatus: "ready"},
	}
	return zones, racks, loads
}

func assignment(load model.EquipmentLoad, rack model.Rack, zoneID uint, zoneCode string) dto.RackAssignment {
	return dto.RackAssignment{
		LoadID: load.ID, LoadName: load.Name, RackID: rack.ID, RackCode: rack.RackCode,
		ZoneID: zoneID, ZoneCode: zoneCode, PowerKW: load.PowerKW, HeatKW: load.HeatKW,
		AirflowCFM: load.AirflowCFM, RackUnits: load.RackUnits,
	}
}

func relocationInput(zones []model.ThermalZone, loads []model.EquipmentLoad, assignments []dto.RackAssignment, load model.EquipmentLoad, rack model.Rack) Relocation {
	zone := model.ThermalZone{}
	for _, candidate := range zones {
		if candidate.ID == rack.ZoneID {
			zone = candidate
		}
	}
	return Relocation{Zones: zones, Loads: loads, Assignments: assignments, Load: load, Rack: rack, Zone: zone}
}

func TestRelocationBlockedConstraints(t *testing.T) {
	zones, racks, loads := relocationFixture()
	oversize := model.EquipmentLoad{ID: 9, Name: "Oversize", PowerKW: 30, HeatKW: 50, AirflowCFM: 9000, RackUnits: 50, RedundancyGroup: "G9", LoadStatus: "ready"}
	allLoads := append(append([]model.EquipmentLoad(nil), loads...), oversize)
	tests := []struct {
		name        string
		load        model.EquipmentLoad
		rack        model.Rack
		assignments []dto.RackAssignment
		wantCode    string
	}{
		{
			name: "rack power limit",
			load: loads[0], rack: racks[0],
			assignments: []dto.RackAssignment{
				assignment(model.EquipmentLoad{ID: 7, Name: "Big", PowerKW: 20, HeatKW: 18, AirflowCFM: 3000, RackUnits: 10, RedundancyGroup: "G7"}, racks[0], 1, "A"),
			},
			wantCode: "RACK_POWER_LIMIT",
		},
		{
			name: "rack airflow limit",
			load: loads[0], rack: racks[0],
			assignments: []dto.RackAssignment{
				assignment(model.EquipmentLoad{ID: 7, Name: "Windy", PowerKW: 4, HeatKW: 4, AirflowCFM: 6500, RackUnits: 4, RedundancyGroup: "G7"}, racks[0], 1, "A"),
			},
			wantCode: "RACK_AIRFLOW_LIMIT",
		},
		{
			name: "rack unit limit",
			load: loads[0], rack: racks[0],
			assignments: []dto.RackAssignment{
				assignment(model.EquipmentLoad{ID: 7, Name: "Tall", PowerKW: 4, HeatKW: 4, AirflowCFM: 900, RackUnits: 40, RedundancyGroup: "G7"}, racks[0], 1, "A"),
			},
			wantCode: "RACK_UNIT_LIMIT",
		},
		{
			name: "rack unavailable",
			load: loads[0], rack: racks[3],
			wantCode: "RACK_UNAVAILABLE",
		},
		{
			name: "zone cooling limit",
			load: oversize, rack: racks[1],
			wantCode: "ZONE_COOLING_LIMIT",
		},
		{
			name: "redundancy peer already in target zone",
			load: loads[0], rack: racks[0],
			assignments: []dto.RackAssignment{
				assignment(loads[2], racks[0], 1, "A"),
			},
			wantCode: "REDUNDANCY_ZONE_COLLISION",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outcome := SimulateRelocation(relocationInput(zones, allLoads, tt.assignments, tt.load, tt.rack))
			if outcome.Feasible {
				t.Fatalf("expected infeasible outcome, got %+v", outcome)
			}
			found := false
			for _, item := range outcome.Violations {
				if item.Code == tt.wantCode && item.Severity == "critical" {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected critical %s evidence, got %+v", tt.wantCode, outcome.Violations)
			}
			if !strings.Contains(outcome.Summary, tt.wantCode) {
				t.Fatalf("summary should name the blocking constraint %s, got %q", tt.wantCode, outcome.Summary)
			}
		})
	}
}

func TestRelocationFitsReportsRackAndZone(t *testing.T) {
	zones, racks, loads := relocationFixture()
	assignments := []dto.RackAssignment{assignment(loads[1], racks[0], 1, "A")}
	outcome := SimulateRelocation(relocationInput(zones, loads, assignments, loads[0], racks[1]))
	if !outcome.Feasible || len(outcome.Violations) != 0 {
		t.Fatalf("expected a clean fit, got feasible=%t violations=%+v", outcome.Feasible, outcome.Violations)
	}
	if outcome.Rack.PowerKW != 5 || outcome.Rack.AirflowCFM != 1000 || outcome.Rack.RackUnits != 4 {
		t.Fatalf("target rack usage should reflect only the moved load, got %+v", outcome.Rack)
	}
	if outcome.Rack.PowerLimitKW != 24 || outcome.Rack.RackUnitLimit != 42 {
		t.Fatalf("rack limits should come from the scenario snapshot, got %+v", outcome.Rack)
	}
	// Zone B direct heat 4.7 plus 0.2 * 9.4 adjacent from zone A: 18 + (6.58/40)*12.
	if outcome.Zone.ZoneID != 2 || outcome.Zone.AssignedHeatKW != 4.7 || outcome.Zone.NeighborHeatKW != 1.88 {
		t.Fatalf("unexpected zone heat split: %+v", outcome.Zone)
	}
	if outcome.Zone.EstimatedReturnC != 19.97 || outcome.Zone.CoolingMarginKW != 33.42 {
		t.Fatalf("unexpected zone thermal estimate: %+v", outcome.Zone)
	}
	if outcome.SourceRackID != 0 {
		t.Fatalf("unplaced load should report no source rack, got %d", outcome.SourceRackID)
	}
}

func TestRelocationVacatesSourceBeforeChecking(t *testing.T) {
	zones, racks, loads := relocationFixture()
	heavy := model.EquipmentLoad{ID: 7, Name: "Heavy", PowerKW: 18, HeatKW: 17, AirflowCFM: 3000, RackUnits: 20, RedundancyGroup: "G7", LoadStatus: "ready"}
	allLoads := append(append([]model.EquipmentLoad(nil), loads...), heavy)
	t.Run("same rack move does not double count the load", func(t *testing.T) {
		assignments := []dto.RackAssignment{
			assignment(loads[0], racks[0], 1, "A"),
			assignment(heavy, racks[0], 1, "A"),
		}
		outcome := SimulateRelocation(relocationInput(zones, allLoads, assignments, loads[0], racks[0]))
		if !outcome.Feasible {
			t.Fatalf("moving within the same rack should fit after vacating, got %+v", outcome.Violations)
		}
		if outcome.Rack.PowerKW != 23 || outcome.Rack.RackUnits != 24 {
			t.Fatalf("rack usage should equal peers plus the moved load once, got %+v", outcome.Rack)
		}
		if outcome.SourceRackID != racks[0].ID {
			t.Fatalf("source rack should be reported, got %d", outcome.SourceRackID)
		}
	})
	t.Run("same zone move does not double count heat or redundancy group", func(t *testing.T) {
		assignments := []dto.RackAssignment{
			assignment(loads[0], racks[0], 1, "A"),
			assignment(heavy, racks[0], 1, "A"),
		}
		outcome := SimulateRelocation(relocationInput(zones, allLoads, assignments, loads[0], racks[2]))
		if !outcome.Feasible {
			t.Fatalf("moving within the same zone should fit after vacating, got %+v", outcome.Violations)
		}
		if outcome.Zone.AssignedHeatKW != 21.7 {
			t.Fatalf("zone heat should count the moved load exactly once, got %+v", outcome.Zone)
		}
	})
}

func TestRelocationMoveOutLowersSourceZoneHeat(t *testing.T) {
	zones, racks, loads := relocationFixture()
	assignments := []dto.RackAssignment{
		assignment(loads[0], racks[0], 1, "A"),
		assignment(loads[1], racks[0], 1, "A"),
	}
	outcome := SimulateRelocation(relocationInput(zones, loads, assignments, loads[0], racks[1]))
	if !outcome.Feasible {
		t.Fatalf("expected feasible cross-zone move, got %+v", outcome.Violations)
	}
	// Zone B sees its own 4.7 plus 0.2 * 9.4 from zone A after the source slot is vacated.
	if outcome.Zone.NeighborHeatKW != 1.88 || outcome.Zone.EstimatedReturnC != 19.97 {
		t.Fatalf("source zone heat should be vacated before propagation, got %+v", outcome.Zone)
	}
}
