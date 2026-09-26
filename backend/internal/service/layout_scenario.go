package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"datacenter-thermal-capacity-planner/backend/internal/audit"
	"datacenter-thermal-capacity-planner/backend/internal/constants"
	"datacenter-thermal-capacity-planner/backend/internal/dto"
	"datacenter-thermal-capacity-planner/backend/internal/model"
	"datacenter-thermal-capacity-planner/backend/internal/planner"
	"datacenter-thermal-capacity-planner/backend/internal/repository"
	"datacenter-thermal-capacity-planner/backend/internal/web"
)

type LayoutScenarioService struct {
	scenarios *repository.LayoutScenarioRepository
	zones     *repository.ThermalZoneRepository
	racks     *repository.RackRepository
	loads     *repository.EquipmentLoadRepository
	engine    *planner.Engine
}

type scenarioInputSnapshot struct {
	LoadIDs          []uint `json:"load_ids"`
	AlgorithmVersion string `json:"algorithm_version"`
}

type scenarioEvaluationSnapshot struct {
	LoadIDs          []uint                `json:"load_ids"`
	AlgorithmVersion string                `json:"algorithm_version"`
	Zones            []model.ThermalZone   `json:"zones"`
	Racks            []model.Rack          `json:"racks"`
	Loads            []model.EquipmentLoad `json:"loads"`
}

func NewLayoutScenarioService(scenarios *repository.LayoutScenarioRepository, zones *repository.ThermalZoneRepository, racks *repository.RackRepository, loads *repository.EquipmentLoadRepository, engine *planner.Engine) *LayoutScenarioService {
	return &LayoutScenarioService{scenarios: scenarios, zones: zones, racks: racks, loads: loads, engine: engine}
}

func (s *LayoutScenarioService) List(ctx context.Context, search, status string, page, size int) ([]dto.ScenarioResponse, int64, error) {
	items, total, err := s.scenarios.List(ctx, search, status, page, size)
	if err != nil {
		return nil, 0, err
	}
	responses := make([]dto.ScenarioResponse, 0, len(items))
	for _, item := range items {
		responses = append(responses, dto.DecodeScenario(item))
	}
	return responses, total, nil
}

func (s *LayoutScenarioService) Get(ctx context.Context, id uint) (dto.ScenarioResponse, error) {
	item, err := s.scenarios.Get(ctx, id)
	if err != nil {
		return dto.ScenarioResponse{}, err
	}
	return dto.DecodeScenario(item), nil
}

func (s *LayoutScenarioService) Create(ctx context.Context, req dto.CreateLayoutScenarioRequest, actor audit.Entry) (dto.ScenarioResponse, error) {
	if err := req.ValidateBusiness(); err != nil {
		return dto.ScenarioResponse{}, web.Unprocessable("INVALID_SCENARIO", err.Error(), err)
	}
	loads, err := s.loads.FindByIDs(ctx, req.LoadIDs)
	if err != nil {
		return dto.ScenarioResponse{}, err
	}
	for _, load := range loads {
		if !load.IsPlannable() {
			return dto.ScenarioResponse{}, web.Unprocessable("LOAD_NOT_READY", fmt.Sprintf("load %d is not ready for planning", load.ID), nil)
		}
	}
	snapshot, err := json.Marshal(scenarioInputSnapshot{LoadIDs: req.LoadIDs, AlgorithmVersion: planner.AlgorithmVersion})
	if err != nil {
		return dto.ScenarioResponse{}, web.Internal(fmt.Errorf("encode scenario input: %w", err))
	}
	item := model.LayoutScenario{
		Name: strings.TrimSpace(req.Name), ScenarioStatus: constants.ScenarioDraft,
		RackAssignmentsJSON: "[]", InputSnapshotJSON: string(snapshot), ZoneResultsJSON: "[]",
		ConstraintViolationsJSON: "[]", AlgorithmVersion: planner.AlgorithmVersion,
		Version: 1, CreatedBy: actor.ActorID,
	}
	actor.Action = "layout_scenario.create"
	actor.EntityType = "layout_scenario"
	actor.AfterSummary = fmt.Sprintf("draft loads=%v algorithm=%s", req.LoadIDs, planner.AlgorithmVersion)
	if err := s.scenarios.Create(ctx, &item, actor); err != nil {
		return dto.ScenarioResponse{}, err
	}
	return dto.DecodeScenario(item), nil
}

func (s *LayoutScenarioService) Evaluate(ctx context.Context, id, version uint, actor audit.Entry) (dto.ScenarioResponse, error) {
	current, err := s.scenarios.Get(ctx, id)
	if err != nil {
		return dto.ScenarioResponse{}, err
	}
	var input scenarioInputSnapshot
	if err := json.Unmarshal([]byte(current.InputSnapshotJSON), &input); err != nil || len(input.LoadIDs) == 0 {
		return dto.ScenarioResponse{}, web.Unprocessable("INVALID_SCENARIO_SNAPSHOT", "scenario input snapshot cannot be evaluated", err)
	}
	zones, err := s.zones.All(ctx)
	if err != nil {
		return dto.ScenarioResponse{}, err
	}
	racks, err := s.racks.All(ctx)
	if err != nil {
		return dto.ScenarioResponse{}, err
	}
	loads, err := s.loads.FindByIDs(ctx, input.LoadIDs)
	if err != nil {
		return dto.ScenarioResponse{}, err
	}
	actor.Action = "layout_scenario.evaluate.start"
	actor.EntityType = "layout_scenario"
	evaluating, err := s.scenarios.BeginEvaluation(ctx, id, version, actor)
	if err != nil {
		return dto.ScenarioResponse{}, err
	}
	result := s.engine.Evaluate(zones, racks, loads)
	assignments, err := json.Marshal(result.Assignments)
	if err != nil {
		return dto.ScenarioResponse{}, web.Internal(fmt.Errorf("encode assignments: %w", err))
	}
	zoneResults, err := json.Marshal(result.ZoneResults)
	if err != nil {
		return dto.ScenarioResponse{}, web.Internal(fmt.Errorf("encode zone results: %w", err))
	}
	violations, err := json.Marshal(result.Violations)
	if err != nil {
		return dto.ScenarioResponse{}, web.Internal(fmt.Errorf("encode violations: %w", err))
	}
	fullSnapshot, err := json.Marshal(scenarioEvaluationSnapshot{
		LoadIDs: input.LoadIDs, AlgorithmVersion: planner.AlgorithmVersion, Zones: zones, Racks: racks, Loads: loads,
	})
	if err != nil {
		return dto.ScenarioResponse{}, web.Internal(fmt.Errorf("encode evaluation snapshot: %w", err))
	}
	update := repository.EvaluationUpdate{
		AssignmentsJSON: string(assignments), SnapshotJSON: string(fullSnapshot),
		ZoneResultsJSON: string(zoneResults), ViolationsJSON: string(violations),
		TotalPowerKW: result.TotalPower, PeakTempC: result.PeakTemp, Score: result.Score,
	}
	actor.Action = "layout_scenario.evaluate.finish"
	actor.BeforeSummary = fmt.Sprintf("algorithm=%s input_loads=%d", planner.AlgorithmVersion, len(loads))
	actor.AfterSummary = fmt.Sprintf("score=%.2f assignments=%d violations=%d", result.Score, len(result.Assignments), len(result.Violations))
	if err := s.scenarios.FinishEvaluation(ctx, evaluating, update, actor); err != nil {
		return dto.ScenarioResponse{}, err
	}
	return s.Get(ctx, id)
}

// RelocationTrial is a read-only what-if: it replays the scenario's frozen
// placement with one load moved into a target rack and reports the resulting
// rack usage, target-zone cooling and estimated return temperature, plus the
// constraints that would block the move. The scenario itself is never changed.
func (s *LayoutScenarioService) RelocationTrial(ctx context.Context, id uint, req dto.RelocationTrialRequest) (dto.RelocationTrialResponse, error) {
	scenario, err := s.scenarios.Get(ctx, id)
	if err != nil {
		return dto.RelocationTrialResponse{}, err
	}
	if !scenario.HasEvaluationResults() {
		return dto.RelocationTrialResponse{}, web.Unprocessable("SCENARIO_NOT_EVALUATED", "relocation trial requires an evaluated scenario with placement results", nil)
	}
	var snapshot scenarioEvaluationSnapshot
	if err := json.Unmarshal([]byte(scenario.InputSnapshotJSON), &snapshot); err != nil || len(snapshot.Zones) == 0 || len(snapshot.Racks) == 0 {
		return dto.RelocationTrialResponse{}, web.Unprocessable("INVALID_SCENARIO_SNAPSHOT", "scenario evaluation snapshot cannot support a relocation trial", err)
	}
	var load *model.EquipmentLoad
	for i := range snapshot.Loads {
		if snapshot.Loads[i].ID == req.LoadID {
			load = &snapshot.Loads[i]
			break
		}
	}
	if load == nil {
		return dto.RelocationTrialResponse{}, web.Unprocessable("LOAD_NOT_IN_SCENARIO", fmt.Sprintf("load %d is not part of scenario %d", req.LoadID, id), nil)
	}
	var rack *model.Rack
	for i := range snapshot.Racks {
		if snapshot.Racks[i].ID == req.RackID {
			rack = &snapshot.Racks[i]
			break
		}
	}
	if rack == nil {
		return dto.RelocationTrialResponse{}, web.Unprocessable("RACK_NOT_IN_SCENARIO", fmt.Sprintf("rack %d was not part of the evaluated scenario input", req.RackID), nil)
	}
	var zone *model.ThermalZone
	for i := range snapshot.Zones {
		if snapshot.Zones[i].ID == rack.ZoneID {
			zone = &snapshot.Zones[i]
			break
		}
	}
	if zone == nil {
		return dto.RelocationTrialResponse{}, web.Unprocessable("INVALID_SCENARIO_SNAPSHOT", "target rack zone is missing from the scenario snapshot", nil)
	}
	var assignments []dto.RackAssignment
	if err := json.Unmarshal([]byte(scenario.RackAssignmentsJSON), &assignments); err != nil {
		return dto.RelocationTrialResponse{}, web.Unprocessable("INVALID_SCENARIO_SNAPSHOT", "scenario assignments cannot be decoded", err)
	}
	outcome := planner.SimulateRelocation(planner.Relocation{
		Zones: snapshot.Zones, Loads: snapshot.Loads, Assignments: assignments,
		Load: *load, Rack: *rack, Zone: *zone,
	})
	response := dto.RelocationTrialResponse{
		ScenarioID: scenario.ID, LoadID: load.ID, LoadName: load.Name,
		SourceRackID: outcome.SourceRackID, SourceRackCode: rackCodeOf(snapshot.Racks, outcome.SourceRackID),
		TargetRackID: rack.ID, TargetRackCode: rack.RackCode,
		TargetZoneID: zone.ID, TargetZoneCode: zone.ZoneCode,
		Rack: outcome.Rack, Zone: outcome.Zone,
		Feasible: outcome.Feasible, Violations: outcome.Violations, Summary: outcome.Summary,
	}
	return response, nil
}

func rackCodeOf(racks []model.Rack, id uint) string {
	for _, rack := range racks {
		if rack.ID == id {
			return rack.RackCode
		}
	}
	return ""
}

func (s *LayoutScenarioService) Transition(ctx context.Context, id uint, req dto.TransitionScenarioRequest, actor audit.Entry) (dto.ScenarioResponse, error) {
	current, err := s.scenarios.Get(ctx, id)
	if err != nil {
		return dto.ScenarioResponse{}, err
	}
	if current.Version != req.Version {
		return dto.ScenarioResponse{}, web.Conflict("SCENARIO_VERSION_CONFLICT", "scenario was changed by another user", nil)
	}
	if !constants.ValidScenarioStatus(req.TargetStatus) || !constants.CanTransitionScenario(current.ScenarioStatus, req.TargetStatus) {
		return dto.ScenarioResponse{}, web.Unprocessable("INVALID_SCENARIO_TRANSITION", fmt.Sprintf("cannot transition scenario from %s to %s", current.ScenarioStatus, req.TargetStatus), nil)
	}
	decoded := dto.DecodeScenario(current)
	if req.TargetStatus == constants.ScenarioApproved && decoded.HasCriticalViolation {
		return dto.ScenarioResponse{}, web.Unprocessable("CRITICAL_VIOLATIONS", "scenario cannot be approved while critical violations remain", nil)
	}
	actor.Action = "layout_scenario.transition"
	if req.TargetStatus == constants.ScenarioApproved {
		actor.Action = "layout_scenario.approve"
	}
	actor.EntityType = "layout_scenario"
	actor.BeforeSummary = string(current.ScenarioStatus)
	actor.AfterSummary = fmt.Sprintf("%s reason=%s", req.TargetStatus, strings.TrimSpace(req.Reason))
	if err := s.scenarios.Transition(ctx, current, req.TargetStatus, actor.ActorID, actor); err != nil {
		return dto.ScenarioResponse{}, err
	}
	return s.Get(ctx, id)
}

func (s *LayoutScenarioService) Compare(ctx context.Context, leftID, rightID uint) (dto.ScenarioComparison, error) {
	left, err := s.Get(ctx, leftID)
	if err != nil {
		return dto.ScenarioComparison{}, err
	}
	right, err := s.Get(ctx, rightID)
	if err != nil {
		return dto.ScenarioComparison{}, err
	}
	summary := []string{
		fmt.Sprintf("Score changed by %.2f points", right.Score-left.Score),
		fmt.Sprintf("Peak return temperature changed by %.2f C", right.PeakTempC-left.PeakTempC),
		fmt.Sprintf("Critical flag changed from %t to %t", left.HasCriticalViolation, right.HasCriticalViolation),
	}
	return dto.ScenarioComparison{
		Left: left, Right: right, ScoreDelta: right.Score - left.Score,
		PowerDeltaKW:  right.TotalPowerKW - left.TotalPowerKW,
		PeakTempDelta: right.PeakTempC - left.PeakTempC, Summary: summary,
	}, nil
}
