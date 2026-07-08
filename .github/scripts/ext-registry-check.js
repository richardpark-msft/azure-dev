// PR extension registry update check - for the most part we can fail this check if:
// - The update has caused the registry.json to be malformed
// - The update changes (materially) more than a single entry (ie, older entries should be considered immutable)
//   - Any tricks here if they never actually pushed an intermediate release?
// - if the pr is submitted by, or approved by, someone in the core azd team then it can go through.

const { readFile } = require('node:fs/promises');

// this is the bit required for GitHub actions.
module.exports = run;

// exported only for tests - we're just adding them as properties to the
// default export (`run`). They'll be ignored by any production callers.
module.exports.forTests = {
  checkIfUnchangedBotPR,
  isApprovedByCoreTeam,
  isSimpleRegistryJsonUpdate: isAllowedRegistryJsonUpdate,
  coreExtensionApprovers,
}

/**
 * Users that, when they approve, bypass any checks in this file.
 */
function coreExtensionApprovers() {
  return new Set([
    "hemarina",
    "JeffreyCA",
    "RickWinter",
    "richardpark-msft",
    "tg-msft",
    "vhvb1989",
  ]);
}

/** 
* Azure SDK bot - automated release PRs are created by this specific account
* @type {{ id: number, type: 'User' }} 
*/
const AZURE_SDK_BOT = {
  // Run `gh api "users/azure-sdk" --jq '.id'` if you have to get this ID later.
  id: 53356347,
  type: 'User',
};

// GitHub action types

/**
 * @typedef {typeof import('@actions/github').context} Context
 * @typedef {ReturnType<typeof import('@actions/github').getOctokit>} Octokit
 * @typedef {typeof import('@actions/core')} Core
 * @typedef {Awaited<ReturnType<Octokit['rest']['pulls']['listReviews']>>['data'][number]} Review
 */

// registry.json's types

/**
 * @typedef {object} ExtensionVersion
 * @property {string} version
 * @property {string[]} capabilities
 *
 * @typedef {object} Extension
 * @property {string} id
 * @property {string} namespace
 * @property {ExtensionVersion[]} versions
 *
 * @typedef {object} RegistryJson
 * @property {Extension[]} extensions
 */

/**
 * @typedef {NonNullable<Context['payload']['pull_request']>} PullRequest
 */

/**
 * @typedef {Review & { user: NonNullable<Review['user']> }} ReviewWithUser
 */

/**
 * Named capture groups produced by the "stringized" registry entry regex
 * used in {@link checkRegistries}.
 *
 * @typedef {object} RegistryEntryMatchGroups
 * @property {string} id
 * @property {string} ns
 * @property {string} version
 * @property {string} capabilities
 */

/**
 * @param {{ github: Octokit, context: Context, core: Core }} args
 */
async function run({ github: octokit, context, core }) {
  try {
    await runImpl({ octokit, context, core });
  } catch (err) {
    core.setFailed(`Internal failure in script: ${err instanceof Error ? err.message : err}`);
  }
}

/**
 * Determines whether this PR's registry.json update can be auto-approved.
 *
 * @param {{ octokit: Octokit, context: Context, core: Core }} args
 */
async function runImpl({ octokit, context, core }) {
  assertHasPullRequest(context);

  // no extra checks needed if a core team member has already approved it.
  if (await isApprovedByCoreTeam({ octokit, context, core, coreTeam: coreExtensionApprovers() })) {
    core.info(`PR was approved by the core team, no further checks needed`)
    return;
  }

  core.info(`Checking other criteria to see if core team approval is required or not`);

  // if the registry update is not just the simple "bump and release next version" then we'll fail
  // this until a core team member has approved it.
  const reasons = await isAllowedRegistryJsonUpdate({ octokit, context, core })
  if (reasons.length > 0) {
    core.info(`PR contains registry updates that require manual review: * ${reasons.join("\n* ")}`)
    return;
  }

  // so at this point we have a simple PR update, created by the bot. No special approvals needed.
  if (await checkIfUnchangedBotPR({ octokit, context, sdkBot: AZURE_SDK_BOT })) {
    core.info("PR is bot submitted and unchanged, can merge with normal approvals.")
  }

  return false;
}

/**
 * Fetches and parses cli/azd/extensions/registry.json at a given ref.
 *
 * @param {{ octokit: Octokit, owner: string, repo: string, ref: string }} args
 * @returns {Promise<RegistryJson>}
 */
async function getRegistryJson({ octokit, owner, repo, ref }) {
  const { data } = await octokit.rest.repos.getContent({
    owner,
    repo,
    path: 'cli/azd/extensions/registry.json',
    ref,
    mediaType: {
      format: "raw",
    }
  });

  if (typeof data !== 'string') {
    throw new Error(`Unable to load cli / azd / extensions / registry.json from ${owner} / ${repo}@${ref} - can't check extensions.json update`);
  }

  return JSON.parse(data);
}

/**
 * Checks to see if the registry update is a simple "bump version and push next version" PR, which are just fine to
 * let go through with normal approvals from anyone.
 * 
 * @param {{ octokit: Octokit, context: Context, core: Core }} args
 * @returns {Promise<string[]>} empty if the update is considered simple, otherwise contains the reasons why it's not.
 */
async function isAllowedRegistryJsonUpdate({ octokit, context }) {
  // we don't want to just auto-allow certain modifications:
  // 1. adding in a new release for the first time - this'll require a core azd approvder
  // 2. changing providers

  assertHasPullRequest(context);
  const pr = context.payload.pull_request;

  /** @type {string[]} */
  const reasons = [];

  const mainRegistry = await getRegistryJson({
    octokit,
    owner: context.repo.owner,
    repo: context.repo.repo,
    ref: 'main',
  });

  // the PR's head may live in a fork, so we need to fetch from the head repo/sha, not
  // context.repo/main.
  const prRegistry = await getRegistryJson({
    octokit,
    owner: pr['head'].repo?.owner?.login ?? context.repo.owner,
    repo: pr['head'].repo?.name ?? context.repo.repo,
    ref: pr['head'].sha,
  });

  checkRegistries(mainRegistry, prRegistry);
  return reasons;
}

/**
 * Compares two versions of the same extension entry, asserting that nothing besides
 * the expected "bump and release" fields have changed.
 *
 * @param {ExtensionVersion} origVersion
 * @param {ExtensionVersion} newVersion
 * @returns {string[]} empty if the versions are equivalent, otherwise a list of diff messages.
 */
function compareExtensionVersions(origVersion, newVersion) {
  /** @type {string[]} */
  const diffs = [];

  const origCapabilities = new Set(origVersion.capabilities);
  const newCapabilities = new Set(newVersion.capabilities);

  if (origCapabilities.symmetricDifference(newCapabilities).size > 0) {
    diffs.push(
      `capabilities are different [${[...origCapabilities].join(", ")}] but got [${[...newCapabilities].join(", ")}]`
    );
  }

  return diffs;
}

/**
 * @param {{ octokit: Octokit, context: Context, core: Core, coreTeam: Set<string> }} args
 * @returns {Promise<boolean>} true if it is approved, false otherwise.
 */
async function isApprovedByCoreTeam({ octokit, context, core, coreTeam }) {
  if (coreTeam == null || coreTeam.size === 0) {
    throw new Error("Invalid parameter - coreteam must be populated");
  }

  assertHasPullRequest(context);

  const reviews = await octokit.paginate(octokit.rest.pulls.listReviews, {
    ...context.repo,
    pull_number: context.payload.pull_request.number,
  });

  // users can have multiple reviews (ie, they requested changes, then they approved), so we'll 
  // make sure we get their absolutely latest review state.

  // NOTE: api docs indicate reviews always come back in chronological order, according to their docs,
  // and `new Map(entries)` keeps the last entry per key - so this is "latest review per core-team user".
  const latestByUser = new Map(
    reviews.filter(hasUser)
      .filter((r) => coreTeam.has(r.user.login))
      .map((r) => /** @type {[string, Review['state']]} */([r.user.login, r.state]))
  );

  // GitHub will take care of blocking the PR if reviewers did a request-changes, for instance.
  const coreApprovals = [...latestByUser].filter(([, v]) => v === 'APPROVED').map(([k]) => k);

  if (coreApprovals != null && coreApprovals.length > 0) {
    core.info(`PR approved by member(s) of the AZD team (${coreApprovals.join(",")})`)
    return true;
  }

  return false;
}

/**
 * @param {{ octokit: Octokit, context: Context, sdkBot: { id: number, type: string} }} args
 */
async function checkIfUnchangedBotPR({ octokit, context, sdkBot }) {
  assertHasPullRequest(context);

  const pr = context.payload.pull_request;

  /** @param {{ id?: number, type?: string } | null | undefined} entity */
  const isBot = (entity) => entity?.id === sdkBot.id && entity?.type === sdkBot.type;

  if (!isBot(pr['user'])) {
    return false;
  }

  // The tail end of the release process creates a PR to update registry.json. This
  // function checks to see if we're dealing with one of those pure Bot PRs - if it is
  // then we don't need to require an azd person to approve it.
  const commits = await octokit.paginate(octokit.rest.pulls.listCommits, {
    ...context.repo,
    pull_number: pr.number,
  });

  // Azure SDK bot PRs just have a single commit.
  if (commits.length !== 1) {
    return false;
  }

  const [c] = commits;

  if (c == null) {
    return false;
  }

  // so apparently there's some very very small edge conditions where author and committer don't
  // line up, so we're just checking them both. In probably all usage it'll be the same.
  return isBot(c.author) && isBot(c.committer);
}

/**
 * Asserts that we're being invoked for a pull request (and is also a typeguard)
 * 
 * @param {Context} context
 * @returns {asserts context is Context & { payload: { pull_request: PullRequest } }}
 */
function assertHasPullRequest(context) {
  if (context.payload.pull_request == null) {
    throw new Error('No pull_request found in event payload. Workflow targeting should only target pull requests.');
  }
}

/**
 * Type guard narrowing a {@link Review} to one that definitely has a `user`.
 *
 * @param {Review} review
 * @returns {review is ReviewWithUser}
 */
function hasUser(review) {
  return review.user != null;
}

/**
 * @param {RegistryJson} currentRegistry
 * @param {RegistryJson} updatedRegistry
*/
function checkRegistries(currentRegistry, updatedRegistry) {
  const reasons = [];

  /** @type { (reg: RegistryJson) => Set<string> } */
  const makeSet = (reg) => {
    /** @type {Set<string>} */
    const set = new Set();

    // we'll just string-ize all the leaves of this tree so we'll have the id, namespace and other version related fields, and just string-compare them.
    for (const extension of reg.extensions) {
      for (const ver of extension.versions) {
        const str = `id:${extension.id} namespace:${extension.namespace} version:${ver.version} capabilities:${ver.capabilities.sort().join(",")}`;
        set.add(str);
      }
    }

    return set;
  }

  const currentSet = makeSet(currentRegistry);
  const updatedSet = makeSet(updatedRegistry);

  const regexp = new RegExp(/^id:(?<id>.+?) namespace:(?<ns>.+?) version:(?<version>.+?) capabilities:(?<capabilities>.+?)$/);

  // look at the new items, see if there are any big changes that would require review
  for (const newItem of updatedSet.difference(currentSet)) {
    const res = newItem.match(regexp);

    if (!res?.groups) {
      continue;
    }

    const newEntry = /** @type {RegistryEntryMatchGroups} */ (res.groups);

    const currentExtBranch = currentRegistry.extensions.find((e) => e.id === newEntry.id && e.namespace === newEntry.ns);

    // Is this a brand new extension? Approval needed.
    if (currentExtBranch == null) {
      reasons.push(`New extension (id: ${newEntry.id}, namespace: ${newEntry.ns}) should be reviewed by the core team, prior to first release`);
      continue;
    }

    // is this changing an older versions? Approval needed.

    // Since it's a new extension version, does it match the previous version's capabilities?
    const lastReleasedVersion = currentExtBranch.versions[currentExtBranch.versions.length - 1];

    const capabilitiesList = newEntry.capabilities.split(",").filter((c) => c.length > 0);
    const { added, removed } = compareSets(lastReleasedVersion?.capabilities ?? [], capabilitiesList);

    if (added.length > 0) {
      // they've added capabilities...
      reasons.push(`Can't add new capabilities to extension ${id} without core team review: ${[added].join(",")}`)
    }

    if (removed.length > 0) {
      // they've removed capabilities...
      reasons.push(`Can't remove existing capabilities without core team review: ${[removed].join(",")}`)
    }
  }

  return reasons
}

/**
 * @param { string[] } origValues
 * @param { string[] } newValues
 * @returns { { added: string[], removed: string[] } }
 */
function compareSets(origValues, newValues) {
  const origSet = new Set(origValues);
  const newSet = new Set(newValues);

  return {
    removed: [...origSet.difference(newSet)],
    added: [...newSet.difference(origSet)]
  }
}
