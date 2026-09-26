// appSettings タブ: ホーム設定ファイル（$HOME/.graph-ops/config.json）由来の
// サーバー/CLI設定（DBパス・作業ファイル置き場・ノード/ワークフロー設定用
// ディレクトリの上書き・チケット一覧のページング行数）と、プロジェクト管理
// （名前とローカルパスの編集・削除）をまとめて扱う。ローカルパスは DB ではなく
// この環境のホーム設定ファイル（projectPaths）に保存される（DFLT-00080）。
//
// 設定の保存先はこの 1 ファイルだけで、キーごとの例外はない（DFLT-00124）。
// 以前は artifactsDir だけが別のファイル（ホーム設定）に書かれ、残りは解決
// された作業ディレクトリ側のファイルに書かれていたため、「保存先」の表示も
// 2 つ必要だった。
import React, { useCallback, useEffect, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, Save, CheckCircle2, XCircle, Trash2, FolderCog, PlugZap } from 'lucide-react';
import {
  AppSettingsFile,
  APP_SETTINGS_WARNINGS,
  DBBackend,
  EffectiveAppSettings,
  MySQLTLSMode,
  Project,
  REDACTED_SECRET_PLACEHOLDER
} from '../../types';
import { fetchAppSettings, saveAppSettings, testMySQLConnection } from '../../lib/settingsApi';
import { apiFetch } from '../../lib/apiFetch';
import { localizedApiErrorMessage, errorMessage } from '../../lib/apiError';
import { httpDataSourceProblem, HTTPDataSourceProblem, normalizeHTTPDataSourceURL } from '../../lib/httpDataSource';
import { StatusLiveRegion } from '../StatusLiveRegion';
import { useLatest } from '../../hooks/useLatest';
import { useSavedFlash } from '../../hooks/useSavedFlash';
import { useConfirmDialog } from '../../hooks/useConfirmDialog';

interface Props {
  projects: Project[];
  onDirtyChange: (dirty: boolean) => void;
  onProjectsChanged: () => void;
  onPaginationPageSizeChanged: (size: number) => void;
  onMyNameChanged: (name: string) => void;
}

interface FormState {
  dbBackend: DBBackend;
  dbPath: string;
  mysqlHost: string;
  mysqlPort: number;
  mysqlDatabase: string;
  mysqlUser: string;
  mysqlPassword: string;
  mysqlTls: MySQLTLSMode;
  mysqlTlsCa: string;
  httpDataSourceUrl: string;
  httpDataSourceToken: string;
  artifactsDir: string;
  teamExtensionsDir: string;
  paginationPageSize: number;
  myName: string;
}

// Mirrors defaultMySQLPort / newMySQLTarget in
// packages/core-go/internal/httpserver/app_settings.go. An emptied port field
// yields Number('') === 0, and the server reads 0 as "unset" and compares it
// against 3306; normalizing here too keeps this UI's "the connection changed"
// answer identical to the one the server is about to give, instead of asking
// for a password retype the server would not have required.
const DEFAULT_MYSQL_PORT = 3306;
const normalizeMySQLPort = (port: number): number => (port === 0 ? DEFAULT_MYSQL_PORT : port);

// Mirrors store.MySQLTLSVerifyFull / NormalizeMySQLTLSMode's "" -> verify-full
// default. There is deliberately no "preferred"/"skip-verify" option here --
// see MySQLTLSMode's doc comment.
const DEFAULT_MYSQL_TLS: MySQLTLSMode = 'verify-full';
const MYSQL_TLS_MODES: MySQLTLSMode[] = ['verify-full', 'verify-ca', 'disabled'];

const emptyForm: FormState = {
  dbBackend: 'sqlite',
  dbPath: '',
  mysqlHost: '',
  mysqlPort: DEFAULT_MYSQL_PORT,
  mysqlDatabase: '',
  mysqlUser: '',
  mysqlPassword: '',
  mysqlTls: DEFAULT_MYSQL_TLS,
  mysqlTlsCa: '',
  httpDataSourceUrl: '',
  httpDataSourceToken: '',
  artifactsDir: '',
  teamExtensionsDir: '',
  paginationPageSize: 10,
  myName: ''
};

const DB_BACKENDS: DBBackend[] = ['sqlite', 'mysql', 'http'];
const DB_BACKEND_LABEL_KEYS: Record<DBBackend, string> = {
  sqlite: 'settings.appSettings.storage.dbBackendSqlite',
  mysql: 'settings.appSettings.storage.dbBackendMysql',
  http: 'settings.appSettings.storage.dbBackendHttp'
};
const HTTP_PROBLEM_HINT_KEYS: Record<HTTPDataSourceProblem, string> = {
  urlRequired: 'settings.appSettings.storage.httpUrlRequiredHint',
  urlInvalid: 'settings.appSettings.storage.httpUrlInvalidHint',
  plaintextRemote: 'settings.appSettings.storage.httpPlaintextRemoteHint',
  tokenRequired: 'settings.appSettings.storage.httpTokenRequiredHint'
};

const toForm = (file: AppSettingsFile): FormState => ({
  dbBackend: file.dbBackend || 'sqlite',
  dbPath: file.dbPath || '',
  mysqlHost: file.mysqlHost || '',
  mysqlPort: file.mysqlPort || DEFAULT_MYSQL_PORT,
  mysqlDatabase: file.mysqlDatabase || '',
  mysqlUser: file.mysqlUser || '',
  mysqlPassword: file.mysqlPassword || '',
  mysqlTls: (file.mysqlTls || DEFAULT_MYSQL_TLS) as MySQLTLSMode,
  mysqlTlsCa: file.mysqlTlsCa || '',
  httpDataSourceUrl: file.httpDataSourceUrl || '',
  httpDataSourceToken: file.httpDataSourceToken || '',
  artifactsDir: file.artifactsDir || '',
  teamExtensionsDir: file.teamExtensionsDir || '',
  paginationPageSize: file.paginationPageSize || 10,
  myName: file.myName || ''
});

// Whether a team settings directory is an absolute path -- a helper for the
// inline hint only. The server has the final word (PUT /api/settings/app
// rejects a non-absolute teamExtensionsDir with 400 VALIDATION_ERROR, using
// Go's filepath.IsAbs on the machine it runs on); this accepts a POSIX path
// ("/...") and a Windows drive-letter (C:\... or C:/...) or UNC
// (\\server\share) path, so it never blocks a value the server could
// accept.
const isAbsoluteDirPath = (value: string): boolean =>
  value.startsWith('/') || /^[A-Za-z]:[\\/]/.test(value) || value.startsWith('\\\\');

// Mirrors packages/core-go/internal/runtimeconfig.IsEnvVarRef's syntax
// exactly (anchored "${NAME}", nothing before/after) so the UI's "loaded
// from an env var" hint agrees with what the backend will actually resolve
// at connection time.
const ENV_VAR_REF_PATTERN = /^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$/;

// Which of the three states the password field is in, which is what decides
// the hint shown under it. 'redacted' is the one the server put there: a
// password is stored, but this UI was never given it (see
// REDACTED_SECRET_PLACEHOLDER) -- the user has to retype it to change it,
// and leaving the field alone keeps it.
type PasswordFieldState = 'redacted' | 'envVar' | 'plaintext';

const passwordFieldState = (value: string): PasswordFieldState => {
  if (value === REDACTED_SECRET_PLACEHOLDER) return 'redacted';
  if (ENV_VAR_REF_PATTERN.test(value)) return 'envVar';
  return 'plaintext';
};

export const AppSettingsEditor: React.FC<Props> = ({
  projects,
  onDirtyChange,
  onProjectsChanged,
  onPaginationPageSizeChanged,
  onMyNameChanged
}) => {
  const { t } = useTranslation();
  const { confirm, confirmDialog } = useConfirmDialog();
  // See src/hooks/useLatest.ts -- keeps `load` below insensitive to
  // language changes (F-1).
  const tRef = useLatest(t);
  // Prefix for the label/field ids below (project rows append p.id).
  const fieldId = useId();
  const [form, setForm] = useState<FormState>(emptyForm);
  const [savedForm, setSavedForm] = useState<FormState>(emptyForm);
  const [effective, setEffective] = useState<EffectiveAppSettings | null>(null);
  // The one file this page reads and writes. '' when the server could not
  // resolve a home directory: it answers an empty string rather than a path
  // that does not exist, and the note below says so in words instead.
  const [configPath, setConfigPath] = useState('');
  // Codes for what the server could not do, from whichever request answered
  // last. A GET raises them too (an unreadable home config is visible before
  // anything is saved), so this is not cleared on load, it is replaced.
  const [warnings, setWarnings] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const { savedFlash, showSavedFlash } = useSavedFlash();

  const [testingConnection, setTestingConnection] = useState(false);
  const [connectionTestResult, setConnectionTestResult] = useState<{ ok: boolean; message: string } | null>(null);

  const [localPathDrafts, setLocalPathDrafts] = useState<Record<string, string>>({});
  const [nameDrafts, setNameDrafts] = useState<Record<string, string>>({});
  const [projectSavingId, setProjectSavingId] = useState<string | null>(null);
  const [projectDeletingId, setProjectDeletingId] = useState<string | null>(null);
  const [projectErrors, setProjectErrors] = useState<Record<string, string>>({});

  const localPathValue = (p: Project) => (p.id in localPathDrafts ? localPathDrafts[p.id] : p.local_path);
  const nameValue = (p: Project) => (p.id in nameDrafts ? nameDrafts[p.id] : p.name);
  const isProjectDirty = (p: Project) => localPathValue(p) !== p.local_path || nameValue(p) !== p.name;
  // An empty local path is valid: saving it unsets the path in this
  // environment. Only the (shared) name is required.
  const isProjectInvalid = (p: Project) => !nameValue(p).trim();

  const formDirty = JSON.stringify(form) !== JSON.stringify(savedForm);
  const anyProjectDirty = projects.some(isProjectDirty);
  const isDirty = formDirty || anyProjectDirty;
  useEffect(() => onDirtyChange(isDirty), [isDirty, onDirtyChange]);

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const data = await fetchAppSettings(tRef.current);
      setForm(toForm(data.file));
      setSavedForm(toForm(data.file));
      setEffective(data.effective);
      setConfigPath(data.config_path);
      setWarnings(data.warnings ?? []);
    } catch (e) {
      setError(errorMessage(e, tRef.current('errors.UNKNOWN')));
    } finally {
      setLoading(false);
    }
  }, [tRef]);

  useEffect(() => { load(); }, [load]);

  const handleSave = async () => {
    setSaving(true);
    setError('');
    try {
      const data = await saveAppSettings(t, form);
      // Re-seed both form and savedForm from the response rather than from
      // what was typed, exactly as load() does. The PUT answers with the
      // same redacted shape as the GET (see newAppSettingsResponse), so a
      // password the user just typed in plaintext comes back as
      // REDACTED_SECRET_PLACEHOLDER -- which is what puts the field back
      // into its 'redacted' state ("already saved") instead of leaving the
      // plaintext on screen under the "this will be stored in plaintext"
      // hint until the next reload.
      //
      // Both states have to move together: updating savedForm alone would
      // leave form holding the plaintext, which keeps the wrong hint *and*
      // makes formDirty true immediately after a successful save. Setting
      // them from the same value also keeps mysqlTargetChanged (which
      // compares form against savedForm) false right after saving, which is
      // correct -- the destination just saved is now the stored one.
      const nextForm = toForm(data.file);
      setForm(nextForm);
      setSavedForm(nextForm);
      setEffective(data.effective);
      setConfigPath(data.config_path);
      setWarnings(data.warnings ?? []);
      // Unlike every other field here, the page size and my-name are pure
      // frontend/display behavior with nothing to restart -- apply them
      // immediately. They come from nextForm, i.e. what actually landed on
      // disk, rather than from the submitted form.
      onPaginationPageSizeChanged(nextForm.paginationPageSize);
      onMyNameChanged(nextForm.myName);
      showSavedFlash();
    } catch (e) {
      setError(errorMessage(e, t('errors.UNKNOWN')));
    } finally {
      setSaving(false);
    }
  };

  const handleSaveProject = async (p: Project) => {
    const localPath = localPathValue(p).trim();
    const name = nameValue(p).trim();
    setProjectSavingId(p.id);
    setProjectErrors(prev => {
      if (!(p.id in prev)) return prev;
      const next = { ...prev };
      delete next[p.id];
      return next;
    });
    try {
      const res = await apiFetch(`/api/projects/${p.id}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, local_path: localPath })
      });
      if (!res.ok) throw new Error(await localizedApiErrorMessage(t, res));
      setLocalPathDrafts(prev => {
        const next = { ...prev };
        delete next[p.id];
        return next;
      });
      setNameDrafts(prev => {
        const next = { ...prev };
        delete next[p.id];
        return next;
      });
      onProjectsChanged();
    } catch (e) {
      setProjectErrors(prev => ({ ...prev, [p.id]: errorMessage(e, t('errors.UNKNOWN')) }));
    } finally {
      setProjectSavingId(null);
    }
  };

  const mysqlSelected = form.dbBackend === 'mysql';
  const mysqlHostInvalid = mysqlSelected && !form.mysqlHost.trim();
  const mysqlDatabaseInvalid = mysqlSelected && !form.mysqlDatabase.trim();
  const mysqlUserInvalid = mysqlSelected && !form.mysqlUser.trim();
  // Mirrors store.ValidateMySQLTLSSettings: verify-ca has nothing to pin
  // trust to without a CA file (accepting one unset would silently fall
  // back to the OS trust store, which lets any publicly-trusted certificate
  // through -- see that function's doc comment).
  const mysqlTlsCaInvalid = mysqlSelected && form.mysqlTls === 'verify-ca' && !form.mysqlTlsCa.trim();
  const mysqlRequiredMissing = mysqlHostInvalid || mysqlDatabaseInvalid || mysqlUserInvalid || mysqlTlsCaInvalid;
  const mysqlPasswordEnvVar = ENV_VAR_REF_PATTERN.exec(form.mysqlPassword)?.[1];
  const mysqlPasswordState = passwordFieldState(form.mysqlPassword);
  // The server only resolves a resent secret (REDACTED_SECRET_PLACEHOLDER
  // for a stored plaintext password, or the stored "${ENV_VAR}" reference's
  // own text for a stored reference) back to the stored value while
  // host/port/database/user still name the connection that secret was saved
  // for -- otherwise resending it would be a way to have the server deliver
  // a secret the browser was never given in full to some other MySQL server
  // (see resolveSubmittedMySQLPassword in internal/httpserver/app_settings.go).
  // Rather than let the request come back as MYSQL_PASSWORD_RETYPE_REQUIRED,
  // say so at the field, and block the two buttons that would hit it.
  // mysqlTls/mysqlTlsCa are part of the destination tuple too (see
  // mysqlTarget's doc comment in internal/httpserver/app_settings.go, and
  // this ticket's (DFLT-00037) finding F-6): both form and savedForm are
  // already normalized by toForm (an unset file value becomes
  // DEFAULT_MYSQL_TLS), so a plain !== comparison agrees with the server's
  // normalized comparison, exactly like the port normalization above.
  // Deliberately compared both directions -- moving *to* a stronger mode
  // counts as a change too, since judging "is this direction safe" would
  // itself be a new decision surface to get wrong.
  const mysqlTargetChanged =
    form.mysqlHost !== savedForm.mysqlHost ||
    normalizeMySQLPort(form.mysqlPort) !== normalizeMySQLPort(savedForm.mysqlPort) ||
    form.mysqlDatabase !== savedForm.mysqlDatabase ||
    form.mysqlUser !== savedForm.mysqlUser ||
    form.mysqlTls !== savedForm.mysqlTls ||
    form.mysqlTlsCa !== savedForm.mysqlTlsCa;
  // "Is the password field still holding exactly what the last GET/PUT put
  // into it?" mirrors the server's own test (submitted ==
  // RedactSecret(stored)) without needing to special-case 'redacted' vs.
  // 'envVar': savedForm.mysqlPassword is always that last redacted/GET-shown
  // value (RedactedSecretPlaceholder for a plaintext secret, or the
  // "${ENV_VAR}" text itself for a reference), so comparing form against
  // savedForm directly answers "did the user leave this field alone?" for
  // both shapes the same way the server's RedactSecret(stored) comparison
  // does. savedForm.mysqlPassword !== '' excludes "nothing stored" (an
  // empty field left alone is just an empty field, never a resend).
  const mysqlPasswordUnchangedFromSaved =
    form.mysqlPassword === savedForm.mysqlPassword && savedForm.mysqlPassword !== '';
  // Deliberately NOT gated on mysqlSelected. The server's check is not either:
  // it rejects any PUT that resends the stored secret alongside a connection
  // other than the stored one, whatever dbBackend says -- and it is right to,
  // since persisting a changed mysqlHost together with the stored secret is
  // the very thing resolveSubmittedMySQLPassword exists to prevent. Gating
  // this on mysqlSelected let the block lift merely by switching the radio to
  // sqlite, so the save went out and came back 400 on a screen that no longer
  // had a password field to retype in. Now the block survives the switch, and
  // saveBlockedReason below spells out how to clear it from that screen.
  const mysqlPasswordRetypeRequired = mysqlPasswordUnchangedFromSaved && mysqlTargetChanged;

  // HTTP custom data source (DFLT-00088). The token follows the MySQL
  // password's rule (see resolveSubmittedHTTPDataSourceToken in
  // internal/httpserver/app_settings.go): the saved token -- the redacted
  // placeholder or the "${ENV_VAR}" reference the GET returned -- is only
  // honoured for the URL it was saved for. So when the URL is changed while
  // the token field still holds that saved value, the field is emptied
  // (handleHTTPURLChange) and the user is asked to type the token for the
  // new URL; putting the URL back restores the saved value.
  const httpSelected = form.dbBackend === 'http';
  const httpURLChanged =
    normalizeHTTPDataSourceURL(form.httpDataSourceUrl) !== normalizeHTTPDataSourceURL(savedForm.httpDataSourceUrl);
  const httpTokenRetypeNeeded = httpURLChanged && savedForm.httpDataSourceToken !== '' && form.httpDataSourceToken === '';
  const httpTokenState = passwordFieldState(form.httpDataSourceToken);
  const httpTokenEnvVar = ENV_VAR_REF_PATTERN.exec(form.httpDataSourceToken)?.[1];
  const httpProblem = httpSelected ? httpDataSourceProblem(form.httpDataSourceUrl, form.httpDataSourceToken !== '') : null;

  const handleHTTPURLChange = (value: string) => {
    setForm(f => {
      const changed = normalizeHTTPDataSourceURL(value) !== normalizeHTTPDataSourceURL(savedForm.httpDataSourceUrl);
      let token = f.httpDataSourceToken;
      if (changed && savedForm.httpDataSourceToken !== '' && token === savedForm.httpDataSourceToken) {
        token = '';
      } else if (!changed && token === '') {
        token = savedForm.httpDataSourceToken;
      }
      return { ...f, httpDataSourceUrl: value, httpDataSourceToken: token };
    });
  };

  const handleTestConnection = async () => {
    setTestingConnection(true);
    setConnectionTestResult(null);
    try {
      const result = await testMySQLConnection(t, {
        mysqlHost: form.mysqlHost,
        mysqlPort: form.mysqlPort,
        mysqlDatabase: form.mysqlDatabase,
        mysqlUser: form.mysqlUser,
        mysqlPassword: form.mysqlPassword,
        mysqlTls: form.mysqlTls,
        mysqlTlsCa: form.mysqlTlsCa
      });
      setConnectionTestResult(
        result.ok
          ? { ok: true, message: t('settings.appSettings.storage.testConnectionSuccess') }
          : { ok: false, message: t('settings.appSettings.storage.testConnectionFailure', { error: result.error || '' }) }
      );
    } catch (e) {
      setConnectionTestResult({ ok: false, message: errorMessage(e, t('errors.UNKNOWN')) });
    } finally {
      setTestingConnection(false);
    }
  };

  // Confirmed through the in-app ConfirmDialog (DFLT-00148), opened on top of
  // the settings modal; see useModalDialog for how the two dialogs share the
  // keyboard.
  const handleDeleteProject = async (p: Project) => {
    const confirmed = await confirm({
      title: t('settings.appSettings.projects.confirmDeleteTitle'),
      message: t('settings.appSettings.projects.confirmDelete', { name: p.name, id: p.id }),
      confirmLabel: t('settings.appSettings.projects.confirmDeleteButton'),
      tone: 'danger',
      testIdPrefix: 'project-delete-confirm'
    });
    if (!confirmed) return;
    setProjectDeletingId(p.id);
    try {
      const res = await apiFetch(`/api/projects/${p.id}`, { method: 'DELETE' });
      if (!res.ok) throw new Error(await localizedApiErrorMessage(t, res));
      onProjectsChanged();
    } catch (e) {
      setProjectErrors(prev => ({ ...prev, [p.id]: errorMessage(e, t('errors.UNKNOWN')) }));
    } finally {
      setProjectDeletingId(null);
    }
  };

  if (loading) {
    return (
      <div className="flex items-center gap-2 text-slate-500 dark:text-slate-400 text-xs py-8 justify-center">
        <Loader2 aria-hidden="true" className="w-4 h-4 animate-spin" /> {t('settings.common.loading')}
      </div>
    );
  }

  const pageSizeInvalid = !Number.isInteger(form.paginationPageSize) || form.paginationPageSize < 1;
  const teamDirTrimmed = form.teamExtensionsDir.trim();
  const teamDirInvalid = teamDirTrimmed !== '' && !isAbsoluteDirPath(teamDirTrimmed);
  const saveBlocked =
    saving ||
    !formDirty ||
    pageSizeInvalid ||
    teamDirInvalid ||
    mysqlRequiredMissing ||
    mysqlPasswordRetypeRequired ||
    httpProblem !== null;
  const testConnectionBlocked = testingConnection || mysqlRequiredMissing || mysqlPasswordRetypeRequired;

  // Why the save button will not act, as text. A `disabled` button is removed
  // from the tab order, so a keyboard or screen reader user never reaches it
  // and learns neither that it is inert nor why -- hence aria-disabled on the
  // button (below) plus this reason, rendered next to it and pointed at by
  // aria-describedby. It is next to the button on purpose: the MySQL hints
  // that explain two of these conditions sit three sections further up, out of
  // sight of a sighted user too.
  //
  // One reason at a time, worst first. The retype case has its own wording for
  // when MySQL is not the selected backend, because then the MySQL block --
  // and with it the password field the usual wording tells you to retype in --
  // is not on screen at all.
  const saveBlockedReason = saving
    ? ''
    : mysqlRequiredMissing
      ? t('settings.appSettings.storage.mysqlRequiredFieldsHint')
      : mysqlPasswordRetypeRequired
        ? t(
            mysqlSelected
              ? 'settings.appSettings.storage.mysqlPasswordRetypeHint'
              : 'settings.appSettings.storage.mysqlPasswordRetypeOtherBackendHint'
          )
        : httpProblem
          ? t(httpTokenRetypeNeeded && httpProblem === 'tokenRequired'
              ? 'settings.appSettings.storage.httpTokenRetypeHint'
              : HTTP_PROBLEM_HINT_KEYS[httpProblem])
          : pageSizeInvalid
          ? t('settings.appSettings.pagination.invalidPageSize')
          : teamDirInvalid
          ? t('settings.appSettings.teamExtensionsDir.notAbsolute')
          : !formDirty
            ? t('settings.common.noChangesToSave')
            : '';

  // Inline status messages (DFLT-00180). Each one is announced through an
  // always-mounted StatusLiveRegion -- a live region mounted together with its
  // text is not reliably announced (SC 4.1.3, see StatusLiveRegion) -- while
  // the visible copy next to its field is aria-hidden. Both read the same
  // variable, '' meaning "not shown", so what is seen and what is announced
  // cannot drift apart.
  //
  // The retype flags are deliberately not gated on the selected backend (see
  // mysqlPasswordRetypeRequired above), but their visible hints live inside
  // that backend's block, so the announced text is gated here to match.
  const mysqlPasswordRetypeMessage =
    mysqlSelected && mysqlPasswordRetypeRequired ? t('settings.appSettings.storage.mysqlPasswordRetypeHint') : '';
  const mysqlTlsCaRequiredMessage = mysqlTlsCaInvalid ? t('settings.appSettings.storage.mysqlTlsCaRequiredHint') : '';
  const mysqlRequiredFieldsMessage = mysqlRequiredMissing ? t('settings.appSettings.storage.mysqlRequiredFieldsHint') : '';
  const connectionTestMessage = mysqlSelected && connectionTestResult ? connectionTestResult.message : '';
  const httpTokenRetypeMessage =
    httpSelected && httpTokenRetypeNeeded ? t('settings.appSettings.storage.httpTokenRetypeHint') : '';
  const httpProblemMessage = httpProblem ? t(HTTP_PROBLEM_HINT_KEYS[httpProblem]) : '';
  const teamDirInvalidMessage = teamDirInvalid ? t('settings.appSettings.teamExtensionsDir.notAbsolute') : '';

  return (
    <div className="flex flex-col gap-4 h-full min-h-0 overflow-auto">
      {confirmDialog}
      {/* 保存失敗はフォーカス移動を伴わずに現れ、しかもスクロールコンテナ最上部の
          ここに出る（下端の保存ボタンを押した直後は視野外になりうる）。読み上げは
          常時マウントの live region が担当する（SC 4.1.3）。 */}
      <StatusLiveRegion message={error} />
      {error && <div aria-hidden="true" className="p-2.5 bg-red-50 dark:bg-red-950 text-red-700 dark:text-red-300 text-[11px] rounded-lg border border-red-200 dark:border-red-900 whitespace-pre-wrap">{error}</div>}

      {/* Where a save lands. The server answers '' when it has no home
          directory to resolve, so name the file in words rather than
          printing an empty path -- and a save in that state fails with
          HOME_CONFIG_UNAVAILABLE rather than silently going nowhere. */}
      <div className="p-2.5 bg-slate-50 dark:bg-slate-800 text-slate-500 dark:text-slate-400 text-[11px] rounded-lg border border-slate-200 dark:border-slate-700">
        {configPath
          ? t('settings.appSettings.restartNote', { path: configPath })
          : t('settings.appSettings.restartNoteNoPath')}
      </div>

      {/* Raised by the GET as well, so a broken home config is visible when
          the page opens rather than only after a save has failed. The path
          is named because repairing or deleting that one file is the only
          thing that fixes it.

          Naming it needs no empty-path branch of its own (unlike the restart
          note above): the server raises this warning only for a file it
          found and failed to read, which means the home directory resolved,
          which is exactly when config_path is non-empty (accessibility
          review A-3). */}
      {warnings.includes(APP_SETTINGS_WARNINGS.homeConfigUnreadable) && (
        <div className="p-2.5 bg-amber-50 dark:bg-amber-950 text-amber-800 dark:text-amber-300 text-[11px] rounded-lg border border-amber-200 dark:border-amber-900">
          {t('settings.appSettings.homeConfigUnreadable', { path: configPath })}
        </div>
      )}

      {/* Storage */}
      <div className="border border-slate-200 dark:border-slate-800 rounded-lg p-3 space-y-2">
        <h3 className="text-xs font-bold text-slate-700 dark:text-slate-300">{t('settings.appSettings.storage.title')}</h3>

        <div>
          <span id={`${fieldId}-db-backend`} className="block text-[10px] font-semibold text-slate-500 dark:text-slate-400 mb-0.5">
            {t('settings.appSettings.storage.dbBackendLabel')}
          </span>
          <div role="radiogroup" aria-labelledby={`${fieldId}-db-backend`} className="flex gap-3">
            {DB_BACKENDS.map(backend => (
              <label key={backend} className="flex items-center gap-1.5 text-xs text-slate-700 dark:text-slate-300">
                <input
                  type="radio"
                  name="dbBackend"
                  checked={form.dbBackend === backend}
                  onChange={() => {
                    setForm(f => ({ ...f, dbBackend: backend }));
                    setConnectionTestResult(null);
                  }}
                />
                {t(DB_BACKEND_LABEL_KEYS[backend])}
              </label>
            ))}
          </div>
          <p className="text-[10px] text-slate-500 dark:text-slate-400 mt-0.5">{t('settings.appSettings.storage.dbBackendSwitchHint')}</p>
          {effective && <p className="text-[10px] text-slate-500 dark:text-slate-400 mt-0.5">{t('settings.appSettings.currentlyInEffect', { value: effective.dbBackend })}</p>}
        </div>

        {form.dbBackend === 'sqlite' ? (
          <div>
            <label htmlFor={`${fieldId}-db-path`} className="block text-[10px] font-semibold text-slate-500 dark:text-slate-400 mb-0.5">{t('settings.appSettings.storage.dbPathLabel')}</label>
            <input
              id={`${fieldId}-db-path`}
              value={form.dbPath}
              onChange={e => setForm(f => ({ ...f, dbPath: e.target.value }))}
              placeholder={effective?.dbPath}
              className="w-full bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs font-mono text-slate-900 dark:text-slate-100"
            />
            {effective && <p className="text-[10px] text-slate-500 dark:text-slate-400 mt-0.5">{t('settings.appSettings.currentlyInEffect', { value: effective.dbPath })}</p>}
          </div>
        ) : form.dbBackend === 'mysql' ? (
          <div className="space-y-2 border border-slate-100 dark:border-slate-800 rounded-lg p-2.5 bg-slate-50/50 dark:bg-slate-900/50">
            <div className="grid grid-cols-3 gap-2">
              <div className="col-span-2">
                <label htmlFor="mysql-host" className="block text-[10px] font-semibold text-slate-500 dark:text-slate-400 mb-0.5">{t('settings.appSettings.storage.mysqlHostLabel')}</label>
                <input
                  id="mysql-host"
                  value={form.mysqlHost}
                  onChange={e => { setForm(f => ({ ...f, mysqlHost: e.target.value })); setConnectionTestResult(null); }}
                  aria-invalid={mysqlHostInvalid}
                  aria-describedby={mysqlHostInvalid ? 'mysql-required-fields-hint' : undefined}
                  className="w-full bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs font-mono text-slate-900 dark:text-slate-100"
                />
              </div>
              <div>
                <label htmlFor="mysql-port" className="block text-[10px] font-semibold text-slate-500 dark:text-slate-400 mb-0.5">{t('settings.appSettings.storage.mysqlPortLabel')}</label>
                <input
                  id="mysql-port"
                  type="number"
                  value={form.mysqlPort}
                  onChange={e => { setForm(f => ({ ...f, mysqlPort: Number(e.target.value) })); setConnectionTestResult(null); }}
                  className="w-full bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs font-mono text-slate-900 dark:text-slate-100"
                />
              </div>
            </div>
            <div>
              <label htmlFor="mysql-database" className="block text-[10px] font-semibold text-slate-500 dark:text-slate-400 mb-0.5">{t('settings.appSettings.storage.mysqlDatabaseLabel')}</label>
              <input
                id="mysql-database"
                value={form.mysqlDatabase}
                onChange={e => { setForm(f => ({ ...f, mysqlDatabase: e.target.value })); setConnectionTestResult(null); }}
                aria-invalid={mysqlDatabaseInvalid}
                aria-describedby={mysqlDatabaseInvalid ? 'mysql-required-fields-hint' : undefined}
                className="w-full bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs font-mono text-slate-900 dark:text-slate-100"
              />
            </div>
            <div>
              <label htmlFor="mysql-user" className="block text-[10px] font-semibold text-slate-500 dark:text-slate-400 mb-0.5">{t('settings.appSettings.storage.mysqlUserLabel')}</label>
              <input
                id="mysql-user"
                value={form.mysqlUser}
                onChange={e => { setForm(f => ({ ...f, mysqlUser: e.target.value })); setConnectionTestResult(null); }}
                aria-invalid={mysqlUserInvalid}
                aria-describedby={mysqlUserInvalid ? 'mysql-required-fields-hint' : undefined}
                className="w-full bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs font-mono text-slate-900 dark:text-slate-100"
              />
            </div>
            <div>
              <label htmlFor="mysql-password" className="block text-[10px] font-semibold text-slate-500 dark:text-slate-400 mb-0.5">{t('settings.appSettings.storage.mysqlPasswordLabel')}</label>
              {/* A stored password is never sent to this UI, so in the
                  'redacted' state the field renders empty (with a "already
                  saved" placeholder) even though the form state still holds
                  the token -- that is what makes saving some other field
                  preserve the password. The first keystroke replaces the
                  token with what the user actually typed. */}
              <input
                id="mysql-password"
                type="password"
                value={mysqlPasswordState === 'redacted' ? '' : form.mysqlPassword}
                placeholder={mysqlPasswordState === 'redacted' ? t('settings.appSettings.storage.mysqlPasswordSavedPlaceholder') : undefined}
                onChange={e => { setForm(f => ({ ...f, mysqlPassword: e.target.value })); setConnectionTestResult(null); }}
                aria-invalid={mysqlPasswordRetypeRequired}
                aria-describedby={mysqlPasswordRetypeRequired ? 'mysql-password-retype-hint' : undefined}
                className="w-full bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs font-mono text-slate-900 dark:text-slate-100"
              />
              <p className="text-[10px] text-slate-500 dark:text-slate-400 mt-0.5">
                {mysqlPasswordState === 'redacted'
                  ? t('settings.appSettings.storage.mysqlPasswordSavedHint')
                  : mysqlPasswordEnvVar
                    ? t('settings.appSettings.storage.mysqlPasswordFromEnvHint', { envVar: mysqlPasswordEnvVar })
                    : t('settings.appSettings.storage.mysqlPasswordPlaintextHint')}
              </p>
              {/* 読み上げと aria-describedby の参照先は、Storage セクション末尾の
                  常時マウントの live region が担当する（SC 4.1.3）。 */}
              {mysqlPasswordRetypeMessage && (
                <p aria-hidden="true" className="text-[10px] text-amber-700 dark:text-amber-400 mt-0.5">
                  {mysqlPasswordRetypeMessage}
                </p>
              )}
            </div>

            <div>
              <span id="mysql-tls-label" className="block text-[10px] font-semibold text-slate-500 dark:text-slate-400 mb-0.5">
                {t('settings.appSettings.storage.mysqlTlsLabel')}
              </span>
              <div role="radiogroup" aria-labelledby="mysql-tls-label" className="flex flex-col gap-1">
                {MYSQL_TLS_MODES.map(mode => (
                  <label key={mode} className="flex items-center gap-1.5 text-xs text-slate-700 dark:text-slate-300">
                    <input
                      type="radio"
                      name="mysqlTls"
                      checked={form.mysqlTls === mode}
                      onChange={() => { setForm(f => ({ ...f, mysqlTls: mode })); setConnectionTestResult(null); }}
                      aria-describedby={
                        mysqlPasswordRetypeRequired
                          ? 'mysql-tls-mode-hint mysql-password-retype-hint'
                          : 'mysql-tls-mode-hint'
                      }
                    />
                    {t(`settings.appSettings.storage.mysqlTls${mode === 'verify-full' ? 'VerifyFull' : mode === 'verify-ca' ? 'VerifyCa' : 'Disabled'}`)}
                  </label>
                ))}
              </div>
              <p
                id="mysql-tls-mode-hint"
                className={`text-[10px] mt-0.5 ${
                  form.mysqlTls === 'disabled' ? 'text-red-600 dark:text-red-400' : 'text-slate-500 dark:text-slate-400'
                }`}
              >
                {t(
                  form.mysqlTls === 'verify-full'
                    ? 'settings.appSettings.storage.mysqlTlsVerifyFullHint'
                    : form.mysqlTls === 'verify-ca'
                      ? 'settings.appSettings.storage.mysqlTlsVerifyCaHint'
                      : 'settings.appSettings.storage.mysqlTlsDisabledWarning'
                )}
              </p>
            </div>

            {form.mysqlTls !== 'disabled' && (
              <div>
                <label htmlFor="mysql-tls-ca" className="block text-[10px] font-semibold text-slate-500 dark:text-slate-400 mb-0.5">
                  {t('settings.appSettings.storage.mysqlTlsCaLabel')}
                </label>
                <input
                  id="mysql-tls-ca"
                  value={form.mysqlTlsCa}
                  onChange={e => { setForm(f => ({ ...f, mysqlTlsCa: e.target.value })); setConnectionTestResult(null); }}
                  aria-invalid={mysqlTlsCaInvalid}
                  aria-describedby={mysqlTlsCaInvalid ? 'mysql-tls-ca-required-hint' : undefined}
                  className="w-full bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs font-mono text-slate-900 dark:text-slate-100"
                />
                <p className="text-[10px] text-slate-500 dark:text-slate-400 mt-0.5">{t('settings.appSettings.storage.mysqlTlsCaHint')}</p>
                {/* 読み上げは Storage セクション末尾の常時マウントの live region（SC 4.1.3）。 */}
                {mysqlTlsCaRequiredMessage && (
                  <p aria-hidden="true" className="text-[10px] text-red-600 dark:text-red-400 mt-0.5">
                    {mysqlTlsCaRequiredMessage}
                  </p>
                )}
              </div>
            )}

            {/* 読み上げは Storage セクション末尾の常時マウントの live region（SC 4.1.3）。 */}
            {mysqlRequiredFieldsMessage && (
              <p aria-hidden="true" className="text-[10px] text-red-600 dark:text-red-400">{mysqlRequiredFieldsMessage}</p>
            )}

            <div className="flex items-center gap-2 pt-1">
              {/* aria-disabled, not disabled: a disabled button leaves the tab
                  order, so the reason for it being inert (the two hints just
                  above, both of which this points at) never reaches a keyboard
                  or screen reader user. The click handler is what actually
                  stops the action. */}
              <button
                onClick={() => {
                  if (testConnectionBlocked) return;
                  handleTestConnection();
                }}
                aria-disabled={testConnectionBlocked}
                aria-describedby={
                  mysqlRequiredMissing
                    ? 'mysql-required-fields-hint'
                    : mysqlPasswordRetypeRequired
                      ? 'mysql-password-retype-hint'
                      : undefined
                }
                className={`px-2.5 py-1 bg-slate-100 dark:bg-slate-800 rounded text-[11px] font-semibold text-slate-700 dark:text-slate-300 flex items-center gap-1 transition ${
                  testConnectionBlocked ? 'opacity-40 cursor-not-allowed' : 'hover:bg-slate-200 dark:hover:bg-slate-700'
                }`}
              >
                {testingConnection ? <Loader2 aria-hidden="true" className="w-3 h-3 animate-spin" /> : <PlugZap aria-hidden="true" className="w-3 h-3" />}
                {testingConnection ? t('settings.appSettings.storage.testingConnection') : t('settings.appSettings.storage.testConnection')}
              </button>
              {/* 読み上げは Storage セクション末尾の常時マウントの live region（SC 4.1.3）。 */}
              {connectionTestResult && connectionTestMessage && (
                <span
                  aria-hidden="true"
                  className={`text-[11px] flex items-center gap-1 ${connectionTestResult.ok ? 'text-emerald-700 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'}`}
                >
                  {connectionTestResult.ok ? <CheckCircle2 aria-hidden="true" className="w-3.5 h-3.5 shrink-0" /> : <XCircle aria-hidden="true" className="w-3.5 h-3.5 shrink-0" />}
                  {connectionTestResult.message}
                </span>
              )}
            </div>
          </div>
        ) : (
          <div className="space-y-2 border border-slate-100 dark:border-slate-800 rounded-lg p-2.5 bg-slate-50/50 dark:bg-slate-900/50">
            <div>
              <label htmlFor={`${fieldId}-http-url`} className="block text-[10px] font-semibold text-slate-500 dark:text-slate-400 mb-0.5">
                {t('settings.appSettings.storage.httpUrlLabel')}
              </label>
              <input
                id={`${fieldId}-http-url`}
                type="url"
                value={form.httpDataSourceUrl}
                onChange={e => handleHTTPURLChange(e.target.value)}
                placeholder="https://example.com/graphops"
                aria-invalid={httpProblem !== null && httpProblem !== 'tokenRequired'}
                aria-describedby={`${fieldId}-http-url-hint${httpProblem && httpProblem !== 'tokenRequired' ? ` ${fieldId}-http-problem` : ''}`}
                className="w-full bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs font-mono text-slate-900 dark:text-slate-100"
              />
              <p id={`${fieldId}-http-url-hint`} className="text-[10px] text-slate-500 dark:text-slate-400 mt-0.5">
                {t('settings.appSettings.storage.httpUrlHint')}
              </p>
              {effective?.httpDataSourceUrl && (
                <p className="text-[10px] text-slate-500 dark:text-slate-400 mt-0.5">
                  {t('settings.appSettings.currentlyInEffect', { value: effective.httpDataSourceUrl })}
                </p>
              )}
            </div>
            <div>
              <label htmlFor={`${fieldId}-http-token`} className="block text-[10px] font-semibold text-slate-500 dark:text-slate-400 mb-0.5">
                {t('settings.appSettings.storage.httpTokenLabel')}
              </label>
              {/* Same 'redacted' handling as the MySQL password field: the
                  saved token is never sent to this UI, so the field renders
                  empty with a "saved" placeholder while the form state keeps
                  the placeholder token (which is what keeps the stored token
                  on save). */}
              <input
                id={`${fieldId}-http-token`}
                type="password"
                autoComplete="off"
                value={httpTokenState === 'redacted' ? '' : form.httpDataSourceToken}
                placeholder={httpTokenState === 'redacted' ? t('settings.appSettings.storage.mysqlPasswordSavedPlaceholder') : undefined}
                onChange={e => setForm(f => ({ ...f, httpDataSourceToken: e.target.value }))}
                aria-invalid={httpProblem === 'tokenRequired'}
                aria-describedby={`${fieldId}-http-token-hint${httpTokenRetypeNeeded ? ` ${fieldId}-http-token-retype` : ''}${httpProblem === 'tokenRequired' ? ` ${fieldId}-http-problem` : ''}`}
                className="w-full bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs font-mono text-slate-900 dark:text-slate-100"
              />
              <p id={`${fieldId}-http-token-hint`} className="text-[10px] text-slate-500 dark:text-slate-400 mt-0.5">
                {httpTokenState === 'redacted'
                  ? t('settings.appSettings.storage.httpTokenSavedHint')
                  : httpTokenEnvVar
                    ? t('settings.appSettings.storage.httpTokenFromEnvHint', { envVar: httpTokenEnvVar })
                    : t('settings.appSettings.storage.httpTokenPlaintextHint')}
              </p>
              {/* 読み上げは Storage セクション末尾の常時マウントの live region（SC 4.1.3）。 */}
              {httpTokenRetypeMessage && (
                <p aria-hidden="true" className="text-[10px] text-amber-700 dark:text-amber-400 mt-0.5">
                  {httpTokenRetypeMessage}
                </p>
              )}
            </div>
            {/* 読み上げは Storage セクション末尾の常時マウントの live region（SC 4.1.3）。 */}
            {httpProblemMessage && (
              <p aria-hidden="true" className="text-[10px] text-red-600 dark:text-red-400">
                {httpProblemMessage}
              </p>
            )}
          </div>
        )}

        {/* 上の MySQL / HTTP ブロックのインラインメッセージの読み上げ役（SC 4.1.3）。
            ブロックの内側に置くとバックエンドの切り替えで器と文言が同時に挿入され、
            条件付きマウントと同じく読み上げが保証されないので、常に描画される
            この外枠に置く。入力欄やボタンの aria-describedby はこの器の id を参照する
            （見た目側は aria-hidden）。sr-only は position:absolute なので space-y
            のレイアウトは変わらない。 */}
        <StatusLiveRegion id="mysql-password-retype-hint" message={mysqlPasswordRetypeMessage} />
        <StatusLiveRegion id="mysql-tls-ca-required-hint" message={mysqlTlsCaRequiredMessage} />
        <StatusLiveRegion id="mysql-required-fields-hint" message={mysqlRequiredFieldsMessage} />
        <StatusLiveRegion message={connectionTestMessage} />
        <StatusLiveRegion id={`${fieldId}-http-token-retype`} message={httpTokenRetypeMessage} />
        <StatusLiveRegion id={`${fieldId}-http-problem`} message={httpProblemMessage} />

        <div>
          <label htmlFor={`${fieldId}-artifacts-dir`} className="block text-[10px] font-semibold text-slate-500 dark:text-slate-400 mb-0.5">{t('settings.appSettings.storage.artifactsDirLabel')}</label>
          <p className="text-[10px] text-slate-500 dark:text-slate-400 mb-1">{t('settings.appSettings.storage.artifactsDirHint')}</p>
          <input
            id={`${fieldId}-artifacts-dir`}
            value={form.artifactsDir}
            onChange={e => setForm(f => ({ ...f, artifactsDir: e.target.value }))}
            placeholder={effective?.artifactsDir}
            className="w-full bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs font-mono text-slate-900 dark:text-slate-100"
          />
          {effective && <p className="text-[10px] text-slate-500 dark:text-slate-400 mt-0.5">{t('settings.appSettings.currentlyInEffect', { value: effective.artifactsDir })}</p>}
        </div>
      </div>

      {/* My Profile -- used by the per-ticket "assign to me"/"unassign"
          buttons (TicketItem.tsx). Unlike the rest of this tab, this takes
          effect immediately (see handleSave's onMyNameChanged call). */}
      <div className="border border-slate-200 dark:border-slate-800 rounded-lg p-3 space-y-2">
        <h3 className="text-xs font-bold text-slate-700 dark:text-slate-300">{t('settings.appSettings.myProfile.title')}</h3>
        <p className="text-[11px] text-slate-500 dark:text-slate-400">{t('settings.appSettings.myProfile.description')}</p>
        <div>
          <label htmlFor={`${fieldId}-my-name`} className="block text-[10px] font-semibold text-slate-500 dark:text-slate-400 mb-0.5">{t('settings.appSettings.myProfile.nameLabel')}</label>
          <input
            id={`${fieldId}-my-name`}
            value={form.myName}
            onChange={e => setForm(f => ({ ...f, myName: e.target.value }))}
            placeholder={t('settings.appSettings.myProfile.namePlaceholder')}
            className="w-full max-w-xs bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs text-slate-900 dark:text-slate-100"
          />
        </div>
      </div>

      {/* Pagination */}
      <div className="border border-slate-200 dark:border-slate-800 rounded-lg p-3 space-y-2">
        <h3 className="text-xs font-bold text-slate-700 dark:text-slate-300">{t('settings.appSettings.pagination.title')}</h3>
        <div>
          <label htmlFor={`${fieldId}-page-size`} className="block text-[10px] font-semibold text-slate-500 dark:text-slate-400 mb-0.5">{t('settings.appSettings.pagination.pageSizeLabel')}</label>
          <input
            id={`${fieldId}-page-size`}
            type="number"
            min={1}
            value={form.paginationPageSize}
            onChange={e => setForm(f => ({ ...f, paginationPageSize: Number(e.target.value) }))}
            className="w-24 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs text-slate-900 dark:text-slate-100"
          />
          {pageSizeInvalid && <p className="text-[10px] text-red-600 dark:text-red-400 mt-0.5">{t('settings.appSettings.pagination.invalidPageSize')}</p>}
        </div>
      </div>

      {/* Team settings directory (teamExtensionsDir, DFLT-00153). The
          personal directory (userExtensionsDir) is not edited here: it is
          $HOME/.graph-ops unless config.json or GRAPH_USER_EXTENSIONS_DIR
          says otherwise, and a save leaves it alone. */}
      <div className="border border-slate-200 dark:border-slate-800 rounded-lg p-3 space-y-2">
        <h3 id={`${fieldId}-team-extensions-dir`} className="text-xs font-bold text-slate-700 dark:text-slate-300">{t('settings.appSettings.teamExtensionsDir.title')}</h3>
        <p id={`${fieldId}-team-extensions-dir-description`} className="text-[11px] text-slate-500 dark:text-slate-400">{t('settings.appSettings.teamExtensionsDir.description')}</p>
        <input
          aria-labelledby={`${fieldId}-team-extensions-dir`}
          aria-describedby={`${fieldId}-team-extensions-dir-description${teamDirInvalid ? ` ${fieldId}-team-extensions-dir-invalid` : ''}`}
          aria-invalid={teamDirInvalid}
          value={form.teamExtensionsDir}
          onChange={e => setForm(f => ({ ...f, teamExtensionsDir: e.target.value }))}
          placeholder={t('settings.appSettings.teamExtensionsDir.placeholder')}
          className="w-full bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs font-mono text-slate-900 dark:text-slate-100"
        />
        {/* 読み上げと aria-describedby の参照先は常時マウントの live region が
            担当する（SC 4.1.3）。見た目側は aria-hidden。 */}
        <StatusLiveRegion id={`${fieldId}-team-extensions-dir-invalid`} message={teamDirInvalidMessage} />
        {teamDirInvalidMessage && (
          <p aria-hidden="true" className="text-[10px] text-red-600 dark:text-red-400 mt-0.5">
            {teamDirInvalidMessage}
          </p>
        )}
        {effective && (
          <p className="text-[10px] text-slate-500 dark:text-slate-400 mt-0.5">
            {t('settings.appSettings.currentlyInEffect', {
              value: effective.teamExtensionsDir || t('settings.appSettings.teamExtensionsDir.notSet')
            })}
          </p>
        )}
      </div>

      <div className="flex justify-end items-center gap-2">
        {/* 保存成功は2秒で消える。読み上げは常時マウントの live region が
            担当する（SC 4.1.3）。 */}
        <StatusLiveRegion message={savedFlash ? t('settings.common.saveSuccess') : ''} />
        {savedFlash && (
          <span aria-hidden="true" className="text-emerald-700 dark:text-emerald-400 text-xs flex items-center gap-1">
            <CheckCircle2 aria-hidden="true" className="w-3.5 h-3.5" /> {t('settings.common.saveSuccess')}
          </span>
        )}
        {saveBlockedReason && (
          <span id="save-blocked-reason" className={`text-[11px] text-right ${formDirty ? 'text-amber-700 dark:text-amber-400' : 'text-slate-500 dark:text-slate-400'}`}>
            {saveBlockedReason}
          </span>
        )}
        {/* aria-disabled, not disabled, for two reasons. (1) The reason the
            button will not act has to be reachable: a disabled button is out
            of the tab order, so neither it nor its aria-describedby is ever
            encountered. (2) A successful save clears formDirty, which would
            disable the very button the user just pressed -- and the browser
            moves focus off a newly disabled element to <body>, dropping a
            keyboard user back to the top of the document. Staying focusable
            keeps focus where the user put it. */}
        <button
          onClick={() => {
            if (saveBlocked) return;
            handleSave();
          }}
          aria-disabled={saveBlocked}
          aria-describedby={saveBlockedReason ? 'save-blocked-reason' : undefined}
          className={`px-4 py-1.5 bg-blue-600 rounded-lg text-xs font-semibold text-white flex items-center gap-1.5 transition ${
            saveBlocked ? 'opacity-50 cursor-not-allowed' : 'hover:bg-blue-700'
          }`}
        >
          {saving ? <Loader2 aria-hidden="true" className="w-3.5 h-3.5 animate-spin" /> : <Save aria-hidden="true" className="w-3.5 h-3.5" />}
          {saving ? t('settings.common.saving') : t('settings.common.save')}
        </button>
      </div>

      {/* Project management */}
      <div className="border border-slate-200 dark:border-slate-800 rounded-lg p-3 space-y-2">
        <h3 className="text-xs font-bold text-slate-700 dark:text-slate-300 flex items-center gap-1.5">
          <FolderCog aria-hidden="true" className="w-3.5 h-3.5" /> {t('settings.appSettings.projects.title')}
        </h3>
        <p className="text-[11px] text-slate-500 dark:text-slate-400">{t('settings.appSettings.projects.description')}</p>
        <p className="text-[11px] text-slate-500 dark:text-slate-400">{t('settings.appSettings.projects.localPathHint')}</p>
        {projects.length === 0 ? (
          <div className="text-center text-slate-500 dark:text-slate-400 text-xs py-6 border border-dashed border-slate-200 dark:border-slate-700 rounded-lg">
            {t('settings.appSettings.projects.empty')}
          </div>
        ) : (
          <div className="space-y-2">
            {projects.map(p => (
              <div key={p.id} data-testid={`project-row-${p.id}`} className="border border-slate-200 dark:border-slate-800 rounded-lg p-2.5 space-y-1.5 bg-white dark:bg-slate-900">
                {/* Name and local path each get a small visible label above
                    the input (the placeholders stay as a hint); items-end
                    keeps the prefix badge and buttons aligned with the
                    inputs. */}
                <div className="flex items-end gap-2">
                  <span className="mb-1 font-mono text-[10px] px-1.5 py-0.5 bg-blue-50 dark:bg-blue-950 text-blue-700 dark:text-blue-300 border border-blue-200 dark:border-blue-800 rounded shrink-0">
                    {p.prefix}
                  </span>
                  <div className="flex-1 min-w-0 flex flex-col">
                    <label htmlFor={`${fieldId}-project-${p.id}-name`} className="block text-[10px] font-semibold text-slate-500 dark:text-slate-400 mb-0.5">
                      {t('settings.appSettings.projects.nameLabel')}
                    </label>
                    <input
                      id={`${fieldId}-project-${p.id}-name`}
                      value={nameValue(p)}
                      onChange={e => setNameDrafts(prev => ({ ...prev, [p.id]: e.target.value }))}
                      placeholder={t('settings.appSettings.projects.nameLabel')}
                      className="w-full bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs font-semibold text-slate-800 dark:text-slate-200"
                    />
                  </div>
                  <button
                    onClick={() => handleDeleteProject(p)}
                    disabled={projectDeletingId === p.id}
                    title={t('settings.appSettings.projects.delete')}
                    className="ml-auto mb-0.5 p-1 text-slate-500 dark:text-slate-400 hover:text-red-600 dark:hover:text-red-400 disabled:opacity-40 shrink-0"
                  >
                    {projectDeletingId === p.id ? <Loader2 aria-hidden="true" className="w-3.5 h-3.5 animate-spin" /> : <Trash2 aria-hidden="true" className="w-3.5 h-3.5" />}
                  </button>
                </div>
                <div className="flex items-end gap-2">
                  <div className="flex-1 min-w-0 flex flex-col">
                    <label htmlFor={`${fieldId}-project-${p.id}-local-path`} className="flex items-center gap-2 text-[10px] font-semibold text-slate-500 dark:text-slate-400 mb-0.5">
                      {t('settings.appSettings.projects.localPathLabel')}
                      {!localPathValue(p) && (
                        <span className="px-1.5 py-0.5 rounded bg-amber-50 dark:bg-amber-950 text-amber-700 dark:text-amber-300 border border-amber-200 dark:border-amber-800 font-medium">
                          {t('settings.appSettings.projects.notSet')}
                        </span>
                      )}
                    </label>
                    <input
                      id={`${fieldId}-project-${p.id}-local-path`}
                      value={localPathValue(p)}
                      onChange={e => setLocalPathDrafts(prev => ({ ...prev, [p.id]: e.target.value }))}
                      placeholder={t('settings.appSettings.projects.notSet')}
                      className="w-full bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs font-mono text-slate-900 dark:text-slate-100"
                    />
                  </div>
                  <button
                    onClick={() => handleSaveProject(p)}
                    disabled={!isProjectDirty(p) || isProjectInvalid(p) || projectSavingId === p.id}
                    className="px-2.5 py-1 bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 disabled:opacity-40 rounded text-[11px] font-semibold text-slate-700 dark:text-slate-300 flex items-center gap-1 shrink-0 transition"
                  >
                    {projectSavingId === p.id ? <Loader2 aria-hidden="true" className="w-3 h-3 animate-spin" /> : <Save aria-hidden="true" className="w-3 h-3" />}
                    {t('settings.common.save')}
                  </button>
                </div>
                {projectErrors[p.id] && <p className="text-[10px] text-red-600 dark:text-red-400">{projectErrors[p.id]}</p>}
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
};
