// Thin client for the settings UI's backend surface (GET/PUT
// /api/settings/catalog, /api/settings/node-types(/{type}), the skill and
// template endpoints, ...) -- see
// packages/core-go/internal/httpserver/settings.go for the actual contract.
// Every call site (NodeTypesEditor/WorkflowEditor/ReviewGatesEditor) shares
// this instead of re-building query strings/error handling independently.
import { TFunction } from 'i18next';
import {
  AppSettingsFile,
  AppSettingsResponse,
  SettingsCatalogResponse,
  SettingsDocument,
  SettingsNodeTypeInfo,
  SettingsNodeTypeTextResponse,
  SettingsReportTemplateResponse,
  SettingsScope,
  SettingsSkillInfo,
  SettingsSkillTextResponse,
  SettingsTemplateTextResponse,
  TestMySQLConnectionResult
} from '../types';
import { apiFetch } from './apiFetch';
import { localizedApiErrorMessage } from './apiError';

// Every endpoint in this module answers the same way: a JSON body on success,
// and on failure a status outside 2xx with the backend's {"error": {...}}
// shape that localizedApiErrorMessage turns into a user-facing string. That
// pair of lines used to be repeated verbatim at the end of all twelve
// exported functions (DFLT-00023 D-4), which made it a per-function decision
// -- and therefore something a thirteenth function could quietly get wrong --
// rather than the module-wide rule it actually is.
//
// The thrown Error carries the *localized* message because these functions
// are called straight from components whose catch blocks render e.message; a
// caller that needs the raw error code should use parseApiError on its own.
async function requestJSON<T>(t: TFunction, path: string, init?: RequestInit): Promise<T> {
  const res = await apiFetch(path, init);
  if (!res.ok) throw new Error(await localizedApiErrorMessage(t, res));
  return res.json() as Promise<T>;
}

// RequestInit for a write carrying a JSON body, so the method/Content-Type/
// JSON.stringify triple is stated once rather than at each write call site.
function jsonBody(method: 'PUT' | 'POST', payload: unknown): RequestInit {
  return {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload)
  };
}

function scopeQuery(scope: SettingsScope, projectId: string): string {
  const params = new URLSearchParams({ scope });
  if (scope === 'project' && projectId) params.set('project_id', projectId);
  return params.toString();
}

export async function fetchSettingsCatalog(
  t: TFunction,
  scope: SettingsScope,
  projectId: string
): Promise<SettingsCatalogResponse> {
  return requestJSON(t, `/api/settings/catalog?${scopeQuery(scope, projectId)}`);
}

export async function saveSettingsCatalog(
  t: TFunction,
  scope: SettingsScope,
  projectId: string,
  document: SettingsDocument
): Promise<{ merged_catalog: SettingsCatalogResponse['merged_catalog'] }> {
  return requestJSON(t, '/api/settings/catalog', jsonBody('PUT', { scope, project_id: projectId, document }));
}

// GET/PUT /api/settings/app -- the "全体設定" app-settings tab (DB path,
// artifacts dir, node/workflow config directory override, ticket-list
// pagination page size). See internal/httpserver/app_settings.go.
export async function fetchAppSettings(t: TFunction): Promise<AppSettingsResponse> {
  return requestJSON(t, '/api/settings/app');
}

export async function saveAppSettings(t: TFunction, file: AppSettingsFile): Promise<AppSettingsResponse> {
  return requestJSON(t, '/api/settings/app', jsonBody('PUT', file));
}

// POST /api/settings/app/test-mysql-connection -- the "接続テスト" button.
// Takes the form's current (possibly unsaved) MySQL fields directly rather
// than reading graph-config.json, so it can be used before saving.
export async function testMySQLConnection(
  t: TFunction,
  mysql: Pick<AppSettingsFile, 'mysqlHost' | 'mysqlPort' | 'mysqlDatabase' | 'mysqlUser' | 'mysqlPassword' | 'mysqlTls' | 'mysqlTlsCa'>
): Promise<TestMySQLConnectionResult> {
  return requestJSON(t, '/api/settings/app/test-mysql-connection', jsonBody('POST', mysql));
}

export async function fetchSettingsNodeTypes(
  t: TFunction,
  projectId: string
): Promise<SettingsNodeTypeInfo[]> {
  const params = new URLSearchParams();
  if (projectId) params.set('project_id', projectId);
  const data = await requestJSON<{ types?: SettingsNodeTypeInfo[] }>(
    t,
    `/api/settings/node-types?${params.toString()}`
  );
  return data.types || [];
}

export async function fetchSettingsNodeType(
  t: TFunction,
  scope: SettingsScope,
  projectId: string,
  nodeType: string
): Promise<SettingsNodeTypeTextResponse> {
  return requestJSON(
    t,
    `/api/settings/node-types/${encodeURIComponent(nodeType)}?${scopeQuery(scope, projectId)}`
  );
}

export async function saveSettingsNodeType(
  t: TFunction,
  scope: SettingsScope,
  projectId: string,
  nodeType: string,
  text: string
): Promise<SettingsNodeTypeTextResponse> {
  return requestJSON(
    t,
    `/api/settings/node-types/${encodeURIComponent(nodeType)}`,
    jsonBody('PUT', { scope, project_id: projectId, text })
  );
}

export async function fetchSettingsSkills(
  t: TFunction,
  projectId: string
): Promise<SettingsSkillInfo[]> {
  const params = new URLSearchParams();
  if (projectId) params.set('project_id', projectId);
  const data = await requestJSON<{ skills?: SettingsSkillInfo[] }>(
    t,
    `/api/settings/skills?${params.toString()}`
  );
  return data.skills || [];
}

export async function fetchSettingsSkill(
  t: TFunction,
  scope: SettingsScope,
  projectId: string,
  name: string
): Promise<SettingsSkillTextResponse> {
  return requestJSON(t, `/api/settings/skills/${encodeURIComponent(name)}?${scopeQuery(scope, projectId)}`);
}

export async function saveSettingsSkill(
  t: TFunction,
  scope: SettingsScope,
  projectId: string,
  name: string,
  text: string
): Promise<SettingsSkillTextResponse> {
  return requestJSON(
    t,
    `/api/settings/skills/${encodeURIComponent(name)}`,
    jsonBody('PUT', { scope, project_id: projectId, text })
  );
}

export async function fetchSettingsReportTemplate(
  t: TFunction,
  scope: SettingsScope,
  projectId: string
): Promise<SettingsReportTemplateResponse> {
  return requestJSON(t, `/api/settings/report-template?${scopeQuery(scope, projectId)}`);
}

export async function saveSettingsReportTemplate(
  t: TFunction,
  scope: SettingsScope,
  projectId: string,
  html: string
): Promise<SettingsReportTemplateResponse> {
  return requestJSON(
    t,
    '/api/settings/report-template',
    jsonBody('PUT', { scope, project_id: projectId, html })
  );
}

// GET/PUT /api/settings/plan-template and /api/settings/review-template --
// the テンプレート tab's 実行計画 / レビュー entries. Unlike the report
// template, the PUT body field is `text` and the content is not validated.
export async function fetchSettingsPlanTemplate(
  t: TFunction,
  scope: SettingsScope,
  projectId: string
): Promise<SettingsTemplateTextResponse> {
  return requestJSON(t, `/api/settings/plan-template?${scopeQuery(scope, projectId)}`);
}

export async function saveSettingsPlanTemplate(
  t: TFunction,
  scope: SettingsScope,
  projectId: string,
  text: string
): Promise<SettingsTemplateTextResponse> {
  return requestJSON(
    t,
    '/api/settings/plan-template',
    jsonBody('PUT', { scope, project_id: projectId, text })
  );
}

export async function fetchSettingsReviewTemplate(
  t: TFunction,
  scope: SettingsScope,
  projectId: string
): Promise<SettingsTemplateTextResponse> {
  return requestJSON(t, `/api/settings/review-template?${scopeQuery(scope, projectId)}`);
}

export async function saveSettingsReviewTemplate(
  t: TFunction,
  scope: SettingsScope,
  projectId: string,
  text: string
): Promise<SettingsTemplateTextResponse> {
  return requestJSON(
    t,
    '/api/settings/review-template',
    jsonBody('PUT', { scope, project_id: projectId, text })
  );
}
