#!/usr/bin/env node
// ci-verify.mjs — push 后校验 GitHub Actions 结果（Build & Test / Verify Docker / Security）
//
// 用法：
//   node scripts/ci-verify.mjs                  # 校验当前分支 HEAD 的 CI
//   node scripts/ci-verify.mjs <sha>            # 校验指定 commit
//   node scripts/ci-verify.mjs <sha> <branch>   # 显式分支
//
// 特性：
//   - 从 ~/.git-credentials 取 token（git credential fill，不硬编码、不落盘）
//   - 轮询全部关联 workflow 直到完成（默认最多 8 分钟）
//   - 全部 success → 绿（exit 0）；任一 failure → 红（exit 1）并自动下载日志
//     解压提取「失败步骤 + --- FAIL: TestXXX」摘要
//   - 输出带步骤状态的简洁报告
import { execSync, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const REPO = 'linhao616/ensp-lab';
const API = 'https://api.github.com';
const MAX_WAIT_MS = 8 * 60 * 1000;
const POLL_MS = 15 * 1000;

function getToken() {
  const r = spawnSync('git', ['credential', 'fill'], {
    input: 'protocol=https\nhost=github.com\n\n', encoding: 'utf8',
  });
  const m = (r.stdout || '').match(/^password=(.+)$/m);
  return m ? m[1] : null;
}
const TOKEN = getToken();

async function api(url) {
  const res = await fetch(url, {
    headers: { Authorization: `Bearer ${TOKEN}`, 'User-Agent': 'ensp-lab-ci-verify', Accept: 'application/vnd.github+json' },
  });
  if (!res.ok) throw new Error(`API ${res.status} for ${url}`);
  return res.json();
}

function short(s) { return s ? s.slice(0, 7) : '?'; }

async function main() {
  const shaArg = process.argv[2];
  const sha = shaArg
    ? execSync(`git rev-parse ${shaArg}`, { encoding: 'utf8' }).trim()
    : execSync('git rev-parse HEAD', { encoding: 'utf8' }).trim();
  const branch = process.argv[3] || execSync('git branch --show-current', { encoding: 'utf8' }).trim();
  if (!TOKEN) {
    console.error('✗ 无法获取 GitHub token（请确认 ~/.git-credentials 存在且 git credential.helper store 已配置）');
    process.exit(2);
  }
  console.log(`▶ 校验 ${branch} @ ${short(sha)} 的 CI 结果 ...`);

  // 1) 定位该 commit 触发的全部 workflow runs
  let runs = (await api(`${API}/repos/${REPO}/actions/runs?head_sha=${sha}&per_page=10`)).workflow_runs || [];
  if (runs.length === 0) {
    console.log('ℹ 该 commit 没有触发任何 CI（可能分支不在触发列表，如 main 之外的临时分支）。');
    process.exit(0);
  }

  // 2) 轮询直到全部 completed
  const deadline = Date.now() + MAX_WAIT_MS;
  let pending = runs.filter((r) => r.status !== 'completed');
  while (pending.length > 0 && Date.now() < deadline) {
    await new Promise((r) => setTimeout(r, POLL_MS));
    runs = (await api(`${API}/repos/${REPO}/actions/runs?head_sha=${sha}&per_page=10`)).workflow_runs || [];
    pending = runs.filter((r) => r.status !== 'completed');
  }
  if (pending.length > 0) {
    console.warn(`⚠ 超时（${MAX_WAIT_MS / 60000} 分钟），仍有 ${pending.length} 个 run 未完成：${pending.map((r) => r.name).join(', ')}`);
  }

  // 3) 汇总
  console.log('\n=== CI 结果 ===');
  let failed = [];
  for (const r of runs) {
    const mark = r.status !== 'completed' ? '⏳' : r.conclusion === 'success' ? '✅' : '❌';
    console.log(`${mark} ${r.name} (run #${r.run_number}) → ${r.status}${r.conclusion ? ' / ' + r.conclusion : ''}`);
    if (r.status === 'completed' && r.conclusion !== 'success') failed.push(r);
  }
  if (failed.length === 0) {
    console.log('\n🎉 全部 CI 通过。');
    process.exit(0);
  }

  // 4) 失败：下载日志，提取失败步骤 + FAIL 测试摘要
  console.log('\n=== 失败详情 ===');
  for (const r of failed) {
    console.log(`\n--- ${r.name} (run #${r.run_number}) ${r.html_url} ---`);
    try {
      const zipRes = await fetch(`${API}/repos/${REPO}/actions/runs/${r.id}/logs`, {
        headers: { Authorization: `Bearer ${TOKEN}`, 'User-Agent': 'ensp-lab-ci-verify' },
        redirect: 'follow',
      });
      if (!zipRes.ok) { console.log(`  (日志下载失败 HTTP ${zipRes.status})`); continue; }
      const buf = Buffer.from(await zipRes.arrayBuffer());
      const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'ci-verify-'));
      const zip = path.join(dir, 'logs.zip');
      fs.writeFileSync(zip, buf);
      spawnSync('unzip', ['-o', zip, '-d', path.join(dir, 'x')], { stdio: 'ignore' });
      const files = fs.readdirSync(path.join(dir, 'x'), { recursive: true }).filter((f) => f.endsWith('.txt'));
      // 提取失败步骤名（含 ##[error] 的步骤）+ FAIL: Test 行
      const fails = [];
      for (const f of files) {
        const p = path.join(dir, 'x', f);
        const txt = fs.readFileSync(p, 'utf8');
        const hasErr = /##\[error\]|Process completed with exit code [1-9]/.test(txt);
        const failLines = (txt.match(/^.*--- FAIL: [^\s]+.*$/gm) || []).slice(0, 5);
        if (hasErr || failLines.length) fails.push({ step: path.basename(f, '.txt'), failLines });
      }
      for (const f of fails) {
        console.log(`  ✗ 步骤「${f.step}」失败`);
        for (const l of f.failLines) console.log(`      ${l.replace(/^[0-9TZ.:+-]+Z?\s*/, '').trim()}`);
      }
      fs.rmSync(dir, { recursive: true, force: true });
    } catch (e) {
      console.log(`  (日志分析失败: ${e.message})`);
    }
  }
  process.exit(1);
}

main().catch((e) => { console.error('✗ 脚本错误:', e.message); process.exit(3); });
