'use strict';

const { spawn } = require('node:child_process');
const { existsSync } = require('node:fs');
const path = require('node:path');

function target(platform, arch) {
  if (!['linux', 'darwin'].includes(platform) || !['x64', 'arm64'].includes(arch)) return null;
  return `${platform}_${arch === 'x64' ? 'amd64' : arch}`;
}

function run(program, options = {}) {
  const platform = target(options.platform || process.platform, options.arch || process.arch);
  if (!platform) {
    console.error(`Invaris does not support ${options.platform || process.platform}/${options.arch || process.arch}; use Linux, macOS, or WSL on x64 or arm64.`);
    process.exitCode = 2;
    return;
  }
  const root = options.root || path.resolve(__dirname, '..');
  const directory = path.join(root, 'platforms', platform);
  const binary = path.join(directory, program);
  if (!existsSync(binary)) {
    console.error(`Invaris executable is missing: ${binary}. Reinstall the package.`);
    process.exitCode = 2;
    return;
  }
  const child = spawn(binary, process.argv.slice(2), {
    stdio: 'inherit',
    env: { ...process.env, PATH: `${directory}${path.delimiter}${process.env.PATH || ''}` },
  });
  const signals = ['SIGINT', 'SIGTERM', 'SIGHUP'];
  const handlers = new Map(signals.map(signal => [signal, () => child.kill(signal)]));
  for (const [signal, handler] of handlers) process.on(signal, handler);
  child.on('error', error => {
    console.error(`Could not start ${program}: ${error.message}`);
    process.exitCode = 2;
  });
  child.on('exit', (code, signal) => {
    for (const [name, handler] of handlers) process.removeListener(name, handler);
    if (signal) process.kill(process.pid, signal);
    else if (code !== null) process.exitCode = code;
  });
}
module.exports = { target, run };
