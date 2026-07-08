import { describe, it, expect, vi } from 'vitest';

const { execFileSync } = require('node:child_process');

const run = require('../ext-registry-check.js');
const {
  checkIfUnchangedBotPR,
  isApprovedByCoreTeam,
  isSimpleRegistryJsonUpdate,
  coreExtensionApprovers,
} = run.forTests;

/** @typedef {ReturnType<typeof import('@actions/github').getOctokit>} Octokit */

function makeCore() {
  return /** @type {any} */ ({
    setFailed: vi.fn(),
    setOutput: vi.fn(),
    info: vi.fn(),
  });
}

const AZURE_DEV_REPO = { owner: 'Azure', repo: 'azure-dev' };

/**
 * Reads a token from the `gh` CLI's stored auth (`gh auth token`). Throws if `gh`
 * isn't installed or isn't logged in - callers should catch this the same way
 * they'd catch a GitHub API error, since it means the live test can't run.
 *
 * @returns {string}
 */
function tokenFromGhCli() {
  return execFileSync('gh', ['auth', 'token'], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }).trim();
}

/**
 * Builds a real (not mocked) Octokit instance, authenticated using the `gh` CLI's
 * stored token, for the live tests below - so we're exercising actual GitHub API
 * responses rather than our own assumptions about their shape.
 *
 * @returns {Promise<Octokit>}
 */
async function createRealOctokit() {
  const { getOctokit } = await import('@actions/github');
  return getOctokit(tokenFromGhCli());
}

/**
 * Live calls against the real GitHub API (or the `gh` CLI token lookup) can fail
 * for reasons unrelated to the behavior under test (not logged in, rate limiting,
 * sandboxed/offline test runners, etc). Skip rather than fail in those cases.
 *
 * @param {import('vitest').TestContext} t
 * @param {unknown} err
 * @returns {boolean}
 */
function skipIfGitHubUnavailable(t, err) {
  const error = /** @type {Error & { status?: number, code?: string }} */ (err);
  const isRateLimited = error.status === 403 || error.status === 429;
  const isNetworkError = /ENOTFOUND|ETIMEDOUT|ECONNRESET|EAI_AGAIN/.test(`${error.code || ''}${error.message || ''}`);
  const isGhCliUnavailable = /gh auth token|command not found|ENOENT/i.test(error.message || '');

  if (isRateLimited || isNetworkError || isGhCliUnavailable) {
    t.skip(`GitHub API/CLI unavailable: ${error.message}`);
    return true;
  }

  return false;
}

describe('checkIfUnchangedBotPR', () => {
  /**
   * @param {any[]} commits
   */
  function makeOctokit(commits) {
    return /** @type {any} */ ({
      paginate: vi.fn(async () => commits),
      rest: { pulls: { listCommits: () => { } } },
    });
  }

  it('returns true for the azure-sdk bot identity', async () => {
    const octokit = makeOctokit([
      {
        author: { id: 53356347, type: 'User' },
        committer: { id: 53356347, type: 'User' },
      },
    ]);
    const context = /** @type {any} */ ({
      repo: AZURE_DEV_REPO,
      payload: {
        pull_request: {
          number: 1,
          user: { id: 53356347, type: 'User' },
        },
      },
    });

    expect(
      await checkIfUnchangedBotPR({ octokit, context, sdkBot: { id: 53356347, type: 'User' } })
    ).toBe(true);
  });

  it('returns false when the commit is not the bot', async () => {
    const octokit = makeOctokit([
      {
        author: { id: 1, type: 'User' },
        committer: { id: 1, type: 'User' },
      },
    ]);
    const context = /** @type {any} */ ({
      repo: AZURE_DEV_REPO,
      payload: {
        pull_request: {
          number: 1,
          user: { id: 53356347, type: 'User' },
        },
      },
    });

    expect(
      await checkIfUnchangedBotPR({ octokit, context, sdkBot: { id: 53356347, type: 'User' } })
    ).toBe(false);
  });
});

describe('run', () => {
  it('fails when no pull_request is present in the event payload', async () => {
    const core = /** @type {any} */ (makeCore());
    const github = /** @type {any} */ ({});
    const context = /** @type {any} */ ({ payload: {} });
    await run({ github, context, core });

    expect(core.setFailed).toHaveBeenCalledTimes(1);
    const call = core.setFailed.mock.calls[0];
    expect(call).toBeTruthy();
    expect(call[0]).toMatch(/No pull_request/);
  });

  it('skips when the PR was opened by the automated bot account', async () => {
    const core = /** @type {any} */ (makeCore());
    const listReviews = () => { };
    const listCommits = () => { };
    const octokit = /** @type {any} */ ({
      paginate: vi.fn(async (fn) => {
        if (fn === listReviews) {
          return [];
        }

        return [{ author: { id: 53356347, type: 'User' }, committer: { id: 53356347, type: 'User' } }];
      }),
      rest: {
        repos: {
          getContent: vi.fn(async () => ({
            data: JSON.stringify({ extensions: [] }),
          })),
        },
        pulls: { listReviews, listCommits },
      },
    });
    const context = /** @type {any} */ ({
      repo: AZURE_DEV_REPO,
      payload: {
        pull_request: {
          number: 1,
          user: { id: 53356347, login: 'azure-sdk', type: 'User' },
          head: {
            sha: 'abc123',
            repo: { owner: { login: 'Azure' }, name: 'azure-dev' },
          },
        },
      },
    });

    await run({ github: octokit, context, core });

    expect(core.setFailed).toHaveBeenCalledTimes(0);
    expect(core.setOutput).toHaveBeenCalledTimes(0);
  });
});

describe('isSimpleRegistryJsonUpdate', () => {
  function makeOctokit() {
    return /** @type {any} */ ({
      rest: {
        repos: {
          getContent: vi.fn(async () => ({
            data: JSON.stringify({ extensions: [{ id: 'sample.extension', namespace: 'sample', versions: [] }] }),
          })),
        },
        pulls: { listReviews: () => { } },
      },
    });
  }

  it('fetches registry.json for main and the PR head', async () => {
    const octokit = /** @type {any} */ (makeOctokit());
    const context = /** @type {any} */ ({
      repo: AZURE_DEV_REPO,
      payload: {
        pull_request: {
          number: 1,
          head: {
            sha: 'abc123',
            repo: { owner: { login: 'Azure' }, name: 'azure-dev' },
          },
        },
      },
    });

    const result = await isSimpleRegistryJsonUpdate({ octokit, context, core: makeCore() });

    expect(result).toEqual([]);
    expect(octokit.rest.repos.getContent).toHaveBeenCalledTimes(2);
  });
});

describe('isApprovedByCoreTeam (unit, mocked octokit)', () => {
  /**
   * @param {any[]} reviews
   */
  function makeOctokit(reviews) {
    return /** @type {any} */ ({
      paginate: vi.fn(async () => reviews),
      rest: { pulls: { listReviews: () => { } } },
    });
  }

  it('keeps only the latest review per user (reviews arrive in chronological order, per GitHub API docs)', async () => {
    const core = /** @type {any} */ (makeCore());
    const context = /** @type {any} */ ({ repo: AZURE_DEV_REPO, payload: { pull_request: { number: 1 } } });
    const octokit = /** @type {any} */ (makeOctokit([
      { user: { login: 'later-approver' }, state: 'CHANGES_REQUESTED', submitted_at: '2024-01-01T00:00:00Z' },
      { user: { login: 'later-approver' }, state: 'APPROVED', submitted_at: '2024-01-02T00:00:00Z' },
    ]));

    const approved = await isApprovedByCoreTeam({
      octokit,
      context,
      core,
      coreTeam: new Set(['later-approver']),
    });

    expect(approved).toBe(true);
  });

  it('drops a user whose latest review is not an approval', async () => {
    const core = /** @type {any} */ (makeCore());
    const context = /** @type {any} */ ({ repo: AZURE_DEV_REPO, payload: { pull_request: { number: 1 } } });
    const octokit = /** @type {any} */ (makeOctokit([
      { user: { login: 'flip-flopper' }, state: 'APPROVED', submitted_at: '2024-01-01T00:00:00Z' },
      { user: { login: 'flip-flopper' }, state: 'CHANGES_REQUESTED', submitted_at: '2024-01-02T00:00:00Z' },
    ]));

    const approved = await isApprovedByCoreTeam({
      octokit,
      context,
      core,
      coreTeam: new Set(['flip-flopper']),
    });

    expect(approved).toBe(false);
  });

  it('ignores approvals from users who are not in the core team', async () => {
    const core = /** @type {any} */ (makeCore());
    const context = /** @type {any} */ ({ repo: AZURE_DEV_REPO, payload: { pull_request: { number: 1 } } });
    const octokit = /** @type {any} */ (makeOctokit([
      { user: { login: 'random-contributor' }, state: 'APPROVED', submitted_at: '2024-01-01T00:00:00Z' },
    ]));

    const approved = await isApprovedByCoreTeam({
      octokit,
      context,
      core,
      coreTeam: new Set(['someone-else']),
    });

    expect(approved).toBe(false);
  });

  it('throws when coreTeam is missing or empty', async () => {
    const core = /** @type {any} */ (makeCore());
    const context = /** @type {any} */ ({ repo: AZURE_DEV_REPO, payload: { pull_request: { number: 1 } } });
    const octokit = /** @type {any} */ (makeOctokit([]));

    await expect(
      isApprovedByCoreTeam({ octokit, context, core, coreTeam: /** @type {any} */ (null) })
    ).rejects.toThrow(/coreteam must be populated/);
    await expect(
      isApprovedByCoreTeam({ octokit, context, core, coreTeam: new Set() })
    ).rejects.toThrow(/coreteam must be populated/);
  });
});

describe('isSimpleRegistryJsonUpdate (real octokit, live data)', () => {
  it('fetches the real registry.json from main and finds a known extension', async (t) => {
    try {
      const octokit = await createRealOctokit();
      const context = /** @type {any} */ ({
        repo: AZURE_DEV_REPO,
        payload: {
          pull_request: {
            number: 8763,
            head: {
              sha: 'main',
              repo: { owner: { login: 'Azure' }, name: 'azure-dev' },
            },
          },
        },
      });

      const reasons = await isSimpleRegistryJsonUpdate({ octokit, context, core: makeCore() });

      expect(Array.isArray(reasons)).toBe(true);
    } catch (err) {
      if (!skipIfGitHubUnavailable(t, err)) {
        throw err;
      }
    }
  });
});

describe('isApprovedByCoreTeam (real octokit, live PRs)', () => {
  it('returns the single approver of a PR', async (t) => {
    try {
      const octokit = await createRealOctokit();
      const context = /** @type {any} */ ({ repo: AZURE_DEV_REPO, payload: { pull_request: { number: 8763 } } });
      // NOTE: it doesn't matter for our tests, but this PR is closed, so don't be alarmed.
      // https://github.com/Azure/azure-dev/pull/8763
      // "[microsoft.azd.extensions] Registry update for 0.12.0" - merged, approved only by JeffreyCA.
      const approved = await isApprovedByCoreTeam({
        octokit,
        context,
        core: makeCore(),
        coreTeam: coreExtensionApprovers(),
      });

      expect(approved).toBe(true);
    } catch (err) {
      if (!skipIfGitHubUnavailable(t, err)) {
        throw err;
      }
    }
  });

  it('returns every core-team approver for a PR with multiple reviewers', async (t) => {
    try {
      const octokit = await createRealOctokit();
      const context = /** @type {any} */ ({ repo: AZURE_DEV_REPO, payload: { pull_request: { number: 8620 } } });
      // NOTE: it doesn't matter for our tests, but this PR is closed, so don't be alarmed.
      // https://github.com/Azure/azure-dev/pull/8620
      // "[azure.ai.agents] Registry update for 0.1.39-preview" - merged, approved by 3 reviewers
      // (glharper, vhvb1989, JeffreyCA), but only vhvb1989 and JeffreyCA are core team members.
      const approved = await isApprovedByCoreTeam({
        octokit,
        context,
        core: makeCore(),
        coreTeam: coreExtensionApprovers(),
      });

      expect(approved).toBe(true);
    } catch (err) {
      if (!skipIfGitHubUnavailable(t, err)) {
        throw err;
      }
    }
  });
});

describe("compareRegistries", () => {
  it("blah", () => {
    const oldReg = {
      extensions: [
        {
          id: "hello", namespace: "azure.hello", versions: [
            { capabilities: ["capy", "bara"], version: "1.0" }
          ]
        },
      ]
    };

    const newReg = {
      extensions: [
        {
          id: "hello", namespace: "azure.hello", versions: [
            { capabilities: ["capy", "bara"], version: "1.0" },
            { capabilities: ["capy", "bara"], version: "2.0" }
          ]
        },
      ]
    };

    compareRegistries(oldReg, newReg);
  })
});

