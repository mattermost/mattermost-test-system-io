export function matchesBranchFilterToken(
  branchFilter: string,
  branch: string,
  ghPrNumber?: number | null,
): boolean {
  if (!branchFilter) return true;
  const bare = branch.replace(/^refs\/heads\//, '').replace(/^refs\/tags\//, '');
  const bareLower = bare.toLowerCase();
  switch (branchFilter) {
    case 'branch:main-master':
    case 'branch:main':
    case 'branch:master':
      return bareLower === 'main' || bareLower === 'master';
    case 'kind:release':
      return bareLower.startsWith('release-');
    case 'kind:pr':
      return (ghPrNumber != null && ghPrNumber > 0) || /^pr-\d+$/i.test(bare);
    default:
      if (branchFilter.startsWith('branch:')) {
        return bare === branchFilter.slice(7);
      }
      return true;
  }
}
