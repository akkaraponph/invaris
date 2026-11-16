'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { spawnSync, spawn } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { target } = require('./lib/launcher');

test('platform selection', () => {
  assert.equal(target('linux', 'x64'), 'linux_amd64');
  assert.equal(target('linux', 'arm64'), 'linux_arm64');
  assert.equal(target('darwin', 'x64'), 'darwin_amd64');
  assert.equal(target('darwin', 'arm64'), 'darwin_arm64');
  assert.equal(target('win32', 'x64'), null);
  assert.equal(target('linux', 'ia32'), null);
});

test('launcher preserves arguments and exit status, and reports invalid targets and missing files', () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'invaris launcher '));
  try {
    const dir = path.join(root, 'platforms', 'linux_amd64');
    fs.mkdirSync(dir, { recursive: true });
    const fixture = path.join(dir, 'invaris');
    fs.writeFileSync(fixture, '#!/bin/sh\nprintf "%s\\n" "$1"\nexit "$2"\n', { mode: 0o755 });
    const launcher = path.resolve(__dirname, 'lib/launcher.js');
    const script = `require(${JSON.stringify(launcher)}).run('invaris', {root: process.env.TEST_ROOT, platform: process.env.TEST_PLATFORM, arch: process.env.TEST_ARCH})`;
    function invoke(platform, arch, code) {
      return spawnSync(process.execPath, ['-e', script, 'argument with spaces', String(code)], {
        encoding: 'utf8', env: { ...process.env, TEST_ROOT: root, TEST_PLATFORM: platform, TEST_ARCH: arch },
      });
    }
    // -e consumes the first argument as argv[1], unlike an installed launcher.
    for (const code of [0, 1, 2]) {
      const result = spawnSync(process.execPath, ['-e', script, 'launcher', 'argument with spaces', String(code)], {
        encoding: 'utf8', env: { ...process.env, TEST_ROOT: root, TEST_PLATFORM: 'linux', TEST_ARCH: 'x64' },
      });
      assert.equal(result.status, code);
      assert.equal(result.stdout, 'argument with spaces\n');
    }
    assert.equal(invoke('win32', 'x64', 0).status, 2);
    assert.match(invoke('win32', 'x64', 0).stderr, /does not support/);
    assert.match(invoke('linux', 'arm64', 0).stderr, /missing/);
  } finally { fs.rmSync(root, { recursive: true, force: true }); }
});

test('launcher forwards termination', async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'invaris-signal-'));
  const dir = path.join(root, 'platforms', 'linux_amd64');
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(path.join(dir, 'invaris'), '#!/bin/sh\necho ready\nexec sleep 30\n', { mode: 0o755 });
  const launcher = path.resolve(__dirname, 'lib/launcher.js');
  const child = spawn(process.execPath, ['-e', `require(${JSON.stringify(launcher)}).run('invaris', {root: ${JSON.stringify(root)}, platform:'linux', arch:'x64'})`]);
  try {
    await new Promise((resolve, reject) => { child.stdout.once('data', resolve); child.once('error', reject); });
    const exited = new Promise(resolve => child.once('exit', (code, signal) => resolve({ code, signal })));
    child.kill('SIGTERM');
    assert.deepEqual(await exited, { code: null, signal: 'SIGTERM' });
  } finally { child.kill('SIGKILL'); fs.rmSync(root, { recursive: true, force: true }); }
});
