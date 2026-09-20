'use strict';
// The Node half of bin/graph-engine.cmd's argument relay (finding CHK-05).
//
// The .cmd shim does not forward its arguments with cmd.exe's all-arguments
// token, because what that token expands to is parsed a second time by
// cmd.exe: `%PATH%` would be substituted, `&` would start a second command,
// and a quote would re-split the rest. graph-engine's own arguments are
// arbitrary text (`add-artifact <ticket> <node> <name> text <body>` carries a
// whole document), so that re-parse is a silent corruption of user content,
// not a theoretical edge case.
//
// So the shim copies each argument -- already split by cmd.exe, so no
// splitting decision is ever made twice -- into GRAPH_OPS_ARG_<n>, with the
// count in GRAPH_OPS_ARGC, and this module puts them back together. Nothing
// here parses a command line: there is no self-token to locate and no quoting
// rule to reimplement, so a `&&`, a PATH-resolved short name or a nested
// cmd.exe cannot be mistaken for part of an argument on THIS side of the
// relay. What the batch side can guarantee is a separate question with a less
// absolute answer: an argument whose own text contains a quote can still flip
// the quoting of the line that copies it, which the shim records as a known
// limitation of batch rather than as something solved.
//
// The relay is Windows-only. resolveArgv ignores these variables everywhere
// else, so on a platform that never runs the .cmd shim there is no way to
// steer graph-engine's arguments by setting an environment variable.

const ARGC_VAR = 'GRAPH_OPS_ARGC';
const ARG_PREFIX = 'GRAPH_OPS_ARG_';

// ARGC_EMPTY_ARGUMENT is what the shim puts in GRAPH_OPS_ARGC instead of a
// count when it found an argument it cannot relay: cmd.exe cannot hold an
// empty string in an environment variable, so an empty argument is
// indistinguishable from the end of the argument list. The shim detects the
// case it must not let pass -- an empty argument with more arguments after it,
// where ending the list early would run a PREFIX of what the user typed -- and
// asks us to refuse instead. It is deliberately not a number, so even a reader
// that only knows argvFromEnv treats it as a relay that cannot be trusted.
const ARGC_EMPTY_ARGUMENT = 'empty-argument';

// EMPTY_ARGUMENT_MESSAGE explains that refusal. It names the workaround
// (`-`, which every graph-engine subcommand taking a body accepts) because
// the limitation is not one the caller can fix by quoting differently.
const EMPTY_ARGUMENT_MESSAGE =
  'an empty command-line argument cannot be passed through the Windows shim, ' +
  'because cmd.exe cannot store an empty string in an environment variable. ' +
  'Nothing was run, rather than running the command without the arguments that ' +
  "follow it. Pass the value on stdin instead, with '-' in its place.";

// argvFromEnv rebuilds the argument list the .cmd shim recorded in env, or
// returns null when env does not hold a complete one -- no GRAPH_OPS_ARGC (the
// ordinary case on every platform but Windows, and on Windows whenever the
// shim was not involved), a count that is not a non-negative integer
// (ARGC_EMPTY_ARGUMENT included), or a gap in the numbering.
//
// null means "no answer", never "no arguments": an empty argument list is the
// empty array, which a caller must keep distinct, because falling back to
// process.argv on a genuine zero-argument invocation is harmless while
// treating an incomplete relay as zero arguments would silently run
// graph-engine with none of what the user typed.
function argvFromEnv(env = process.env) {
  const raw = env[ARGC_VAR];
  // Deliberately strict: /^\d+$/ rejects '', 'abc', '-1', '1.5' and ' 1'
  // alike. A malformed count means the relay is not trustworthy, and
  // guessing at it is exactly the kind of second interpretation this module
  // exists to avoid.
  if (typeof raw !== 'string' || !/^\d+$/.test(raw)) return null;
  const argc = Number(raw);
  const argv = [];
  for (let i = 1; i <= argc; i += 1) {
    const value = env[`${ARG_PREFIX}${i}`];
    // Only an absent variable is a gap. An empty string is a real value and
    // is kept -- though cmd.exe itself cannot produce one, which is what
    // ARGC_EMPTY_ARGUMENT is about.
    if (value === undefined) return null;
    argv.push(value);
  }
  return argv;
}

// stripArgEnv deletes the relay's own variables from env, in place. It runs
// whether or not argvFromEnv found a usable set, and on every platform, so a
// leftover GRAPH_OPS_ARG_<n> can neither reach graph-engine (which knows
// nothing about them) nor, more importantly, be inherited by anything
// graph-engine itself spawns and be mistaken there for a fresh relay. The
// shim's own scratch variable (GRAPH_OPS_ARG_PEEK) shares the prefix and goes
// the same way.
function stripArgEnv(env = process.env) {
  delete env[ARGC_VAR];
  for (const key of Object.keys(env)) {
    if (key.startsWith(ARG_PREFIX)) delete env[key];
  }
  return env;
}

// resolveArgv is what the shim calls. It answers with both the arguments to
// run (`argv`) and, when the relay asked to be refused, why (`error`: a
// message for stderr, null in every other case). The relay variables are
// cleared before it returns, on every path.
//
// platform is a parameter so tests can exercise the Windows path on the
// machines we actually run tests on; nothing but a test passes it.
//
// Off Windows the relay is not consulted at all, neither for arguments nor
// for the refusal: only bin/graph-engine.cmd ever sets these variables, and
// leaving them live elsewhere would mean anyone able to set an environment
// variable could replace the command a user typed -- `list --json` becoming
// `delete-ticket <id> --yes` -- with nothing on screen to show it.
//
// On Windows the relay wins over process.argv. Via the .cmd shim process.argv
// carries no arguments at all, so in practice the two never both have
// something to say; pinning the order this way (rather than the reverse)
// means the shim can never end up running a mixture of the two, and
// stripArgEnv having just run is what keeps a stale relay from outliving the
// invocation that set it -- scripts/resolve-binary.js starts this same script
// with real process arguments on win32, and that call must not be steered by
// variables some earlier command left behind.
function resolveArgv(env = process.env, processArgv = process.argv.slice(2), platform = process.platform) {
  const relaying = platform === 'win32';
  const error = relaying && env[ARGC_VAR] === ARGC_EMPTY_ARGUMENT ? EMPTY_ARGUMENT_MESSAGE : null;
  const relayed = relaying ? argvFromEnv(env) : null;
  stripArgEnv(env);
  return { argv: relayed === null ? processArgv : relayed, error };
}

module.exports = {
  argvFromEnv,
  stripArgEnv,
  resolveArgv,
  ARGC_VAR,
  ARG_PREFIX,
  ARGC_EMPTY_ARGUMENT,
  EMPTY_ARGUMENT_MESSAGE,
};
