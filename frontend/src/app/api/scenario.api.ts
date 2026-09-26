import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { ApiClient, ApiPage } from './api-client';
import { LayoutScenario, RelocationTrial, ScenarioComparison, ScenarioStatus } from '../../types/scenario';

@Injectable({providedIn: 'root'})
export class ScenarioApi {
  private readonly api = inject(ApiClient);
  list(status = ''): Observable<ApiPage<LayoutScenario>> { return this.api.get('/scenarios', {status, size: 200}); }
  get(id: number): Observable<LayoutScenario> { return this.api.get(`/scenarios/${id}`); }
  create(name: string, loadIds: number[]): Observable<LayoutScenario> { return this.api.post('/scenarios', {name, load_ids: loadIds}); }
  evaluate(id: number, version: number): Observable<LayoutScenario> { return this.api.post(`/scenarios/${id}/evaluate`, {version}); }
  transition(id: number, version: number, target: ScenarioStatus, reason: string): Observable<LayoutScenario> { return this.api.post(`/scenarios/${id}/transition`, {version, target_status: target, reason}); }
  compare(leftId: number, rightId: number): Observable<ScenarioComparison> { return this.api.get(`/scenarios/${leftId}/compare`, {right_id: rightId}); }
  relocationTrial(scenarioId: number, loadId: number, rackId: number): Observable<RelocationTrial> {
    return this.api.get(`/scenarios/${scenarioId}/relocation-trial`, {load_id: loadId, rack_id: rackId});
  }
}
