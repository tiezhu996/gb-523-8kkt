package dto

import (
	"encoding/json"
	"errors"
	"strings"

	"datacenter-thermal-capacity-planner/backend/internal/constants"
	"datacenter-thermal-capacity-planner/backend/internal/model"
)

type CreateLayoutScenarioRequest struct {
	Name    string `json:"name" binding:"required,min=3,max=120"`
	LoadIDs []uint `json:"load_ids" binding:"required,min=1"`
}

type EvaluateScenarioRequest struct {
	Version uint `json:"version" binding:"required"`
}

type TransitionScenarioRequest struct {
	TargetStatus constants.ScenarioStatus `json:"target_status" binding:"required"`
	Version      uint                     `json:"version" binding:"required"`
	Reason       string                   `json:"reason" binding:"max=500"`
}

// EvaluationSnapshot is the full evaluated input frozen at evaluation time.
// Relocation trials are read-only projections against this snapshot so that
// later edits to racks, zones or loads cannot silently change an old scenario.
type EvaluationSnapshot struct {
	LoadIDs          []uint                `json:"load_ids"`
	AlgorithmVersion string                `json:"algorithm_version"`
	Zones            []model.ThermalZone   `json:"zones"`
	Racks            []model.Rack          `json:"racks"`
	Loads            []model.EquipmentLoad `json:"loads"`
}

type ConstraintViolation struct {
	Code       string  `json:"code"`
	Severity   string  `json:"severity"`
	EntityType string  `json:"entity_type"`
	EntityID   uint    `json:"entity_id"`
	Message    string  `json:"message"`
	Actual     float64 `json:"actual"`
	Limit      float64 `json:"limit"`
}

type RackAssignment struct {
	LoadID         uint     `json:"load_id"`
	LoadName       string   `json:"load_name"`
	RackID         uint     `json:"rack_id"`
	RackCode       string   `json:"rack_code"`
	ZoneID         uint     `json:"zone_id"`
	ZoneCode       string   `json:"zone_code"`
	PowerKW        float64  `json:"power_kw"`
	HeatKW         float64  `json:"heat_kw"`
	AirflowCFM     float64  `json:"airflow_cfm"`
	RackUnits      int      `json:"rack_units"`
	PlacementScore float64  `json:"placement_score"`
	Explanation    []string `json:"explanation"`
}

type ZoneThermalResult struct {
	ZoneID             uint    `json:"zone_id"`
	ZoneCode           string  `json:"zone_code"`
	AssignedHeatKW     float64 `json:"assigned_heat_kw"`
	NeighborHeatKW     float64 `json:"neighbor_heat_kw"`
	EstimatedReturnC   float64 `json:"estimated_return_c"`
	TemperatureMarginC float64 `json:"temperature_margin_c"`
	CoolingMarginKW    float64 `json:"cooling_margin_kw"`
}

type ScenarioResponse struct {
	ID                   uint                     `json:"id"`
	Name                 string                   `json:"name"`
	ScenarioStatus       constants.ScenarioStatus `json:"scenario_status"`
	LoadIDs              []uint                   `json:"load_ids"`
	Evaluated            bool                     `json:"evaluated"`
	Assignments          []RackAssignment         `json:"assignments"`
	ZoneResults          []ZoneThermalResult      `json:"zone_results"`
	Violations           []ConstraintViolation    `json:"violations"`
	TotalPowerKW         float64                  `json:"total_power_kw"`
	PeakTempC            float64                  `json:"peak_temp_c"`
	Score                float64                  `json:"score"`
	Version              uint                     `json:"version"`
	AlgorithmVersion     string                   `json:"algorithm_version"`
	CreatedBy            uint                     `json:"created_by"`
	ApprovedBy           *uint                    `json:"approved_by"`
	HasCriticalViolation bool                     `json:"has_critical_violation"`
}

type ScenarioComparison struct {
	Left          ScenarioResponse `json:"left"`
	Right         ScenarioResponse `json:"right"`
	ScoreDelta    float64          `json:"score_delta"`
	PowerDeltaKW  float64          `json:"power_delta_kw"`
	PeakTempDelta float64          `json:"peak_temp_delta_c"`
	Summary       []string         `json:"summary"`
}

// RelocationTrialRequest selects one evaluated scenario, one of its loads and
// a target rack for the read-only relocation what-if.
type RelocationTrialRequest struct {
	LoadID uint `json:"load_id" form:"load_id" binding:"required"`
	RackID uint `json:"rack_id" form:"rack_id" binding:"required"`
}

type RelocationLoad struct {
	ID              uint    `json:"id"`
	Name            string  `json:"name"`
	PowerKW         float64 `json:"power_kw"`
	HeatKW          float64 `json:"heat_kw"`
	AirflowCFM      float64 `json:"airflow_cfm"`
	RackUnits       int     `json:"rack_units"`
	RedundancyGroup string  `json:"redundancy_group"`
}

type RelocationPlacement struct {
	PowerKW    float64 `json:"power_kw"`
	HeatKW     float64 `json:"heat_kw"`
	AirflowCFM float64 `json:"airflow_cfm"`
	RackUnits  int     `json:"rack_units"`
}

type RelocationUsage struct {
	PowerKW    float64 `json:"power_kw"`
	AirflowCFM float64 `json:"airflow_cfm"`
	RackUnits  int     `json:"rack_units"`
}

type RelocationRackTrial struct {
	RackID          uint                `json:"rack_id"`
	RackCode        string              `json:"rack_code"`
	ZoneID          uint                `json:"zone_id"`
	ZoneCode        string              `json:"zone_code"`
	RackStatus      string              `json:"rack_status"`
	PowerLimitKW    float64             `json:"power_limit_kw"`
	AirflowLimitCFM float64             `json:"airflow_limit_cfm"`
	RackUnitsLimit  int                 `json:"rack_units_limit"`
	OccupiedBefore  RelocationUsage     `json:"occupied_before"`
	Vacated         RelocationUsage     `json:"vacated"`
	Projected       RelocationPlacement `json:"projected"`
	PowerHeadroomKW float64             `json:"power_headroom_kw"`
	AirflowHeadroom float64             `json:"airflow_headroom_cfm"`
	UnitsHeadroom   int                 `json:"units_headroom"`
}

type RelocationZoneTrial struct {
	ZoneID                   uint    `json:"zone_id"`
	ZoneCode                 string  `json:"zone_code"`
	ZoneStatus               string  `json:"zone_status"`
	CoolingCapacityKW        float64 `json:"cooling_capacity_kw"`
	AssignedHeatBefore       float64 `json:"assigned_heat_before_kw"`
	RemainingHeatAfterVacate float64 `json:"remaining_heat_after_vacate_kw"`
	ProjectedHeatKW          float64 `json:"projected_heat_kw"`
	CoolingMarginKW          float64 `json:"cooling_margin_kw"`
	SupplyTempC              float64 `json:"supply_temp_c"`
	EstimatedReturnC         float64 `json:"estimated_return_c"`
	MaxReturnTempC           float64 `json:"max_return_temp_c"`
	TemperatureMarginC       float64 `json:"temperature_margin_c"`
}

type RelocationSource struct {
	HasPlacement bool            `json:"has_placement"`
	SameRack     bool            `json:"same_rack"`
	RackID       uint            `json:"rack_id"`
	RackCode     string          `json:"rack_code"`
	ZoneID       uint            `json:"zone_id"`
	ZoneCode     string          `json:"zone_code"`
	Remaining    RelocationUsage `json:"remaining_after_vacate"`
}

type RelocationTrialResponse struct {
	ScenarioID          uint                  `json:"scenario_id"`
	AlgorithmVersion    string                `json:"algorithm_version"`
	Load                RelocationLoad        `json:"load"`
	Source              RelocationSource      `json:"source"`
	TargetRack          RelocationRackTrial   `json:"target_rack"`
	TargetZone          RelocationZoneTrial   `json:"target_zone"`
	Violations          []ConstraintViolation `json:"violations"`
	Feasible            bool                  `json:"feasible"`
	BlockingConstraints []string              `json:"blocking_constraints"`
}

func (r CreateLayoutScenarioRequest) ValidateBusiness() error {
	if strings.TrimSpace(r.Name) == "" {
		return errors.New("scenario name is required")
	}
	seen := map[uint]bool{}
	for _, id := range r.LoadIDs {
		if id == 0 {
			return errors.New("load ids must be positive")
		}
		if seen[id] {
			return errors.New("load ids must not contain duplicates")
		}
		seen[id] = true
	}
	return nil
}

func DecodeScenario(value model.LayoutScenario) ScenarioResponse {
	response := ScenarioResponse{
		ID: value.ID, Name: value.Name, ScenarioStatus: value.ScenarioStatus,
		TotalPowerKW: value.TotalPowerKW, PeakTempC: value.PeakTempC,
		Score: value.Score, Version: value.Version, AlgorithmVersion: value.AlgorithmVersion,
		CreatedBy: value.CreatedBy, ApprovedBy: value.ApprovedBy,
		LoadIDs:     []uint{},
		Assignments: []RackAssignment{}, ZoneResults: []ZoneThermalResult{}, Violations: []ConstraintViolation{},
	}
	var snapshot EvaluationSnapshot
	if err := json.Unmarshal([]byte(value.InputSnapshotJSON), &snapshot); err == nil && len(snapshot.LoadIDs) > 0 {
		response.LoadIDs = snapshot.LoadIDs
		response.Evaluated = len(snapshot.Racks) > 0
	}
	_ = json.Unmarshal([]byte(value.RackAssignmentsJSON), &response.Assignments)
	_ = json.Unmarshal([]byte(value.ZoneResultsJSON), &response.ZoneResults)
	_ = json.Unmarshal([]byte(value.ConstraintViolationsJSON), &response.Violations)
	for _, violation := range response.Violations {
		if violation.Severity == "critical" {
			response.HasCriticalViolation = true
			break
		}
	}
	return response
}
