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
// here parses a command line: there is no self-token to locate, no quoting
// rule to reimplement, and therefore no way for a `&&`, a PATH-resolved short
// name, or a nested cmd.exe to be read as part of our arguments.

const ARGC_VAR = 'GRAPH_OPS_ARGC';
const ARG_PREFIX = 'GRAPH_OPS_ARG_';

// argvFromEnv rebuilds the argument list the .cmd shim recorded in env, or
// returns null when env does not hold a complete one -- no GRAPH_OPS_ARGC (the
// ordinary case on every platform but Windows, and on Windows whenever the
// shim was not involved), a count that is not a non-negative integer, or a
// gap in the numbering.
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
    // is kept -- though cmd.exe itself cannot produce one (assigning an empty
    // value deletes the variable), which is the one documented limitation of
    // the relay: an empty argument reaches here as a gap, and the caller
    // falls back to process.argv instead of dropping the rest.
    if (value === undefined) return null;
    argv.push(value);
  }
  return argv;
}

// stripArgEnv deletes the relay's own variables from env, in place. It runs
// whether or not argvFromEnv found a usable set, so a leftover
// GRAPH_OPS_ARG_<n> can neither reach graph-engine (which knows nothing about
// them) nor, more importantly, be inherited by anything graph-engine itself
// spawns and be mistaken there for a fresh relay.
function stripArgEnv(env = process.env) {
  delete env[ARGC_VAR];
  for (const key of Object.keys(env)) {
    if (key.startsWith(ARG_PREFIX)) delete env[key];
  }
  return env;
}

// resolveArgv is what the shim calls: the relayed arguments when env carries a
// complete set, otherwise the process's own, with the relay variables cleared
// either way.
//
// The relay wins over process.argv. Via the .cmd shim process.argv carries no
// arguments at all, so in practice the two never both have something to say;
// pinning the order this way (rather than the reverse) means the shim can
// never end up running a mixture of the two, and stripArgEnv having just run
// is what keeps a stale relay from outliving the invocation that set it --
// scripts/resolve-binary.js starts this same script with real process
// arguments on win32, and that call must not be steered by variables some
// earlier command left behind.
function resolveArgv(env = process.env, processArgv = process.argv.slice(2)) {
  const relayed = argvFromEnv(env);
  stripArgEnv(env);
  return relayed === null ? processArgv : relayed;
}

module.exports = { argvFromEnv, stripArgEnv, resolveArgv, ARGC_VAR, ARG_PREFIX };
