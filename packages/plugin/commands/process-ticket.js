#!/usr/bin/env node
const { spawnSync } = require('child_process');
const { resolveBinary } = require('../scripts/resolve-binary');

const args = process.argv.slice(2);
const { command, args: prefixArgs } = resolveBinary();
const res = spawnSync(command, [...prefixArgs, 'get-executable', ...args], { stdio: 'inherit' });
process.exit(res.status || 0);
