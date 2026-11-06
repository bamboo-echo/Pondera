import { readFileSync, existsSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const problems = [];

const mustExist = [
  'www/index.html',
  'capacitor.config.json',
  'package.json'
];
for (const rel of mustExist) {
  if (!existsSync(join(root, rel))) problems.push(`缺少文件: ${rel}`);
}

try {
  const cfg = JSON.parse(readFileSync(join(root, 'capacitor.config.json'), 'utf8'));
  if (!cfg.appId || !cfg.appName || cfg.webDir !== 'www') {
    problems.push('capacitor.config.json 的 appId/appName/webDir 配置不完整');
  }
  if (!cfg.server || !cfg.server.allowNavigation || !cfg.server.allowNavigation.includes('*')) {
    problems.push('server.allowNavigation 必须允许外部域名（手机端页面由云端 serve 提供）');
  }
  if (cfg.server?.cleartext === true) {
    problems.push('公网环境不要开启 cleartext');
  }
} catch (e) {
  problems.push('capacitor.config.json 不是合法 JSON: ' + e.message);
}

try {
  const pkg = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8'));
  if (!pkg.dependencies?.['@capacitor/core'] || !pkg.devDependencies?.['@capacitor/cli']) {
    problems.push('package.json 缺少 @capacitor/core / @capacitor/cli');
  }
} catch (e) {
  problems.push('package.json 不是合法 JSON: ' + e.message);
}

const html = readFileSync(join(root, 'www/index.html'), 'utf8');
if (!html.includes('/mobile')) problems.push('引导页没有跳转到 /mobile');
if (!html.includes('localStorage')) problems.push('引导页缺少本地配置保存');

if (problems.length) {
  console.error('校验失败:');
  for (const p of problems) console.error('  - ' + p);
  process.exit(1);
}
console.log('✓ Capacitor 工程配置有效');
console.log('  appId    : io.pondera.mobile');
console.log('  webDir   : www（本地引导页）');
console.log('  页面来源 : 云端 serve /mobile（allowNavigation 已放开）');
console.log('下一步: npm install && npx cap add android && npx cap sync && npx cap open android');
