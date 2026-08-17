// cliCompleteClient.ts
// ─────────────────────────────────────────────────────────────────────────────
// 专用于处理 CLI Tab 补全请求的后端端点：
//   POST /api/topologies/:topoId/devices/:deviceId/cli/complete
//
// 该端点契约（见 internal/api/cli_handlers.go: completeCLI）：
//   请求体: { "view": string, "sub": string, "input": string }   // 例: {view:"user", sub:"", input:"dis"}
//   响应体: { "candidates": string[] }                           // 例: {candidates:["ip","ipv6","version"]}
//   仅计算补全候选，绝不执行命令、零副作用（设计 §3.2 / AC4）。
//
// 本模块在原生 fetch 之上补齐三件原 api.completeCli 缺失的能力：
//   1. 显式配置 referrerPolicy = "strict-origin-when-cross-origin"（安全策略要求）。
//   2. 单次请求超时（AbortController），避免连接挂起无限等待。
//   3. 指数退避重试 + 错误分类：网络错误/超时/5xx/429 可重试；4xx（含 400 请求体
//      错误、404 路由缺失）不可重试、立即抛出，避免无谓重试。
//
// 背景：此前在浏览器中触发 Tab 补全偶发 "Failed to fetch"（network 错误）。根因多为
// 后端进程被本地安全软件（360/Defender 对未签名+网络能力 Go 二进制的启发式误报）
// 终止，前端 fetch 直接抛 network 异常。重试机制可在进程被短暂终止/重启期间自动恢复，
// 但无法修复“进程持续被拦”的情形（需把 ensp-lab.exe 加入安全软件信任区）。
// ─────────────────────────────────────────────────────────────────────────────

export interface CliCompleteRequest {
  /** 当前 CLI 视图：user / system 等（后端仅用于本次补全计算，不持久化） */
  view: string;
  /** 子视图上下文（多为空串 ""） */
  sub: string;
  /** 当前已输入的命令行，如 "dis" 或 "dis ip" */
  input: string;
}

export interface CliCompleteResponse {
  candidates: string[];
}

export interface CliCompleteOptions {
  topoId: string;
  deviceId: string;
  /** 接口前缀，默认空串（同源）；跨域部署时传入后端 origin，如 "http://127.0.0.1:8080" */
  baseUrl?: string;
  /** 最大重试次数（不含首次），默认 3 → 共最多 4 次尝试 */
  retries?: number;
  /** 首次退避基数（毫秒），默认 300；后续 300→600→1200 指数增长 */
  baseDelayMs?: number;
  /** 单次请求超时（毫秒），默认 5000 */
  timeoutMs?: number;
  /** Referrer 策略，默认 "strict-origin-when-cross-origin" */
  referrerPolicy?: ReferrerPolicy;
}

/** 补全请求的分类错误，便于上层区分“可重试”与“致命” */
export class CliCompleteError extends Error {
  constructor(
    message: string,
    public readonly kind: 'network' | 'timeout' | 'http' | 'parse',
    /** 该错误是否应触发重试 */
    public readonly retryable: boolean,
    /** HTTP 状态码（仅 kind==='http' 时有值） */
    public readonly status?: number,
  ) {
    super(message);
    this.name = 'CliCompleteError';
  }
}

/** 5xx 服务端错误、429 限流、0（fetch 网络层失败，status 缺失）视为可重试 */
function isRetryableStatus(status: number): boolean {
  return status === 0 || (status >= 500 && status < 600) || status === 429;
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/**
 * 向 /cli/complete 发起一次（带重试的）补全请求。
 *
 * @returns 候选字符串数组（解析失败或空响应返回空数组）
 * @throws CliCompleteError 在穷尽重试后仍失败，或遇到不可重试错误（4xx）时抛出
 */
export async function requestCliComplete(
  body: CliCompleteRequest,
  opts: CliCompleteOptions,
): Promise<string[]> {
  const {
    topoId,
    deviceId,
    baseUrl = '',
    retries = 3,
    baseDelayMs = 300,
    timeoutMs = 5000,
    referrerPolicy = 'strict-origin-when-cross-origin',
  } = opts;

  const url =
    `${baseUrl}/api/topologies/${encodeURIComponent(topoId)}` +
    `/devices/${encodeURIComponent(deviceId)}/cli/complete`;

  let lastErr: CliCompleteError | null = null;

  for (let attempt = 0; attempt <= retries; attempt++) {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);

    try {
      const res = await fetch(url, {
        method: 'POST',
        // 注意：Referrer-Policy 通常是“响应头”，用于约束后续请求的 Referrer 泄露。
        // 这里通过 fetch 的 referrerPolicy 选项控制“本次请求如何携带 Referrer”，
        // 满足 strict-origin-when-cross-origin 的安全策略要求（同源请求也显式声明）。
        referrerPolicy,
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(body),
        signal: controller.signal,
      });
      clearTimeout(timer);

      if (!res.ok) {
        const err = new CliCompleteError(
          `补全请求 HTTP ${res.status}`,
          'http',
          isRetryableStatus(res.status),
          res.status,
        );
        lastErr = err;
        // 4xx（含 400 请求体错误、404）不可重试 → 立即抛出，不再退避
        if (!err.retryable) throw err;
      } else {
        const text = await res.text();
        if (!text) return [];
        try {
          const data = JSON.parse(text) as CliCompleteResponse;
          return Array.isArray(data.candidates) ? data.candidates : [];
        } catch {
          const err = new CliCompleteError('补全响应 JSON 解析失败', 'parse', false, undefined);
          lastErr = err;
          throw err; // 解析错误不可重试
        }
      }
    } catch (e) {
      clearTimeout(timer);
      // 已分类的 CliCompleteError 直接向上抛
      if (e instanceof CliCompleteError) throw e;

      // fetch 网络错误 / AbortController 超时（"Failed to fetch" 落在此分支）
      const isTimeout = e instanceof DOMException && e.name === 'AbortError';
      const err = new CliCompleteError(
        isTimeout ? '补全请求超时' : '网络请求失败（Failed to fetch）',
        isTimeout ? 'timeout' : 'network',
        true, // 网络/超时均可重试
        undefined,
      );
      lastErr = err;
    }

    // 到达此处说明需要重试：退避后再试（最后一次不再 sleep）
    if (attempt < retries) {
      const backoff = baseDelayMs * 2 ** attempt + Math.random() * 100; // 指数退避 + 抖动
      await sleep(backoff);
    }
  }

  throw lastErr ?? new CliCompleteError('未知补全失败', 'network', true, undefined);
}
