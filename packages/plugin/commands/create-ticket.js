#!/usr/bin/env node
const { spawnSync } = require('child_process');
const { resolveBinary } = require('../scripts/resolve-binary');

const args = process.argv.slice(2);
const res = spawnSync(resolveBinary(), ['create-ticket', ...args], { stdio: 'inherit' });
process.exit(res.status || 0);
