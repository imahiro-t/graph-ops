// Thin client for the settings UI's backend surface (GET/PUT
// /api/settings/catalog, /api/settings/node-types(/{type}), the skill and
// template endpoints, ...) -- see
// packages/core-go/internal/httpserver/settings.go for the actual contract.
// Every call site (the editors under components/settings/ -- NodeTypesEditor,
// ReviewGatesEditor, SkillsEditor, TemplatesEditor, ReportTemplateEditor,
// AppSettingsEditor -- plus labelsApi) shares this instead of re-building
// query strings/error handling independently.
import { TFunction } from 'i18next';
import {
  AppSettingsFile,
  AppSettingsResponse,
  SettingsCatalogResponse,
  SettingsDocument,
  SettingsNodeTypeInfo,
  SettingsNodeTypeTextResponse,
  SettingsReportTemplateResponse,
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
// pair of lines used to be repeated verbatim at the end of all seventeen
// exported functions below (DFLT-00023 D-4), which made it a per-function
// decision -- and therefore something the next function added could quietly
// get wrong -- rather than the module-wide rule it actually is.
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

// Every endpoint below addresses one tier -- the user tier -- so none of
// them takes a scope or a project id. Per-project settings were removed in
// DFLT-00124: the server no longer reads `scope`/`project_id` from a query
// string or a request body, so sending either would be a parameter nothing
// acts on.
export async function fetchSettingsCatalog(t: TFunction): Promise<SettingsCatalogResponse> {
  return requestJSON(t, '/api/settings/catalog');
}

export async function saveSettingsCatalog(
  t: TFunction,
  document: SettingsDocument
): Promise<{ merged_catalog: SettingsCatalogResponse['merged_catalog'] }> {
  return requestJSON(t, '/api/settings/catalog', jsonBody('PUT', { document }));
}

// GET/PUT /api/settings/app -- the "アプリ設定" app-settings tab (DB path,
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
// than reading the saved config, so it can be used before saving.
export async function testMySQLConnection(
  t: TFunction,
  mysql: Pick<AppSettingsFile, 'mysqlHost' | 'mysqlPort' | 'mysqlDatabase' | 'mysqlUser' | 'mysqlPassword' | 'mysqlTls' | 'mysqlTlsCa'>
): Promise<TestMySQLConnectionResult> {
  return requestJSON(t, '/api/settings/app/test-mysql-connection', jsonBody('POST', mysql));
}

export async function fetchSettingsNodeTypes(t: TFunction): Promise<SettingsNodeTypeInfo[]> {
  const data = await requestJSON<{ types?: SettingsNodeTypeInfo[] }>(t, '/api/settings/node-types');
  return data.types || [];
}

export async function fetchSettingsNodeType(
  t: TFunction,
  nodeType: string
): Promise<SettingsNodeTypeTextResponse> {
  return requestJSON(t, `/api/settings/node-types/${encodeURIComponent(nodeType)}`);
}

export async function saveSettingsNodeType(
  t: TFunction,
  nodeType: string,
  text: string
): Promise<SettingsNodeTypeTextResponse> {
  return requestJSON(
    t,
    `/api/settings/node-types/${encodeURIComponent(nodeType)}`,
    jsonBody('PUT', { text })
  );
}

export async function fetchSettingsSkills(t: TFunction): Promise<SettingsSkillInfo[]> {
  const data = await requestJSON<{ skills?: SettingsSkillInfo[] }>(t, '/api/settings/skills');
  return data.skills || [];
}

export async function fetchSettingsSkill(
  t: TFunction,
  name: string
): Promise<SettingsSkillTextResponse> {
  return requestJSON(t, `/api/settings/skills/${encodeURIComponent(name)}`);
}

export async function saveSettingsSkill(
  t: TFunction,
  name: string,
  text: string
): Promise<SettingsSkillTextResponse> {
  return requestJSON(
    t,
    `/api/settings/skills/${encodeURIComponent(name)}`,
    jsonBody('PUT', { text })
  );
}

export async function fetchSettingsReportTemplate(
  t: TFunction
): Promise<SettingsReportTemplateResponse> {
  return requestJSON(t, '/api/settings/report-template');
}

export async function saveSettingsReportTemplate(
  t: TFunction,
  html: string
): Promise<SettingsReportTemplateResponse> {
  return requestJSON(t, '/api/settings/report-template', jsonBody('PUT', { html }));
}

// GET/PUT /api/settings/plan-template and /api/settings/review-template --
// the テンプレート tab's 実行計画 / レビュー entries. Unlike the report
// template, the PUT body field is `text` and the content is not validated.
export async function fetchSettingsPlanTemplate(
  t: TFunction
): Promise<SettingsTemplateTextResponse> {
  return requestJSON(t, '/api/settings/plan-template');
}

export async function saveSettingsPlanTemplate(
  t: TFunction,
  text: string
): Promise<SettingsTemplateTextResponse> {
  return requestJSON(t, '/api/settings/plan-template', jsonBody('PUT', { text }));
}

export async function fetchSettingsReviewTemplate(
  t: TFunction
): Promise<SettingsTemplateTextResponse> {
  return requestJSON(t, '/api/settings/review-template');
}

export async function saveSettingsReviewTemplate(
  t: TFunction,
  text: string
): Promise<SettingsTemplateTextResponse> {
  return requestJSON(t, '/api/settings/review-template', jsonBody('PUT', { text }));
}
