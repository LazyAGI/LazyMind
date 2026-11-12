"""Select one core CI shard from the packages printed by go list ./...."""
import argparse
import sys


PACKAGES = {
    # Spread the three long-running packages from #966 across separate runners.
    'root': {'lazymind/core', 'lazymind/core/academic'},
    'chat': {'lazymind/core/chat', 'lazymind/core/migrations'},
    'workflow': {'lazymind/core/workflow', 'lazymind/core/migrate'},
}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('shard', choices=[*PACKAGES, 'other'])
    args = parser.parse_args()
    packages = [line.strip() for line in sys.stdin if line.strip()]
    if args.shard == 'other':
        assigned = set().union(*PACKAGES.values())
        selected = [package for package in packages if package not in assigned]
    else:
        selected = [package for package in packages if package in PACKAGES[args.shard]]
    if not selected:
        parser.error(f'no packages selected for core shard {args.shard}')
    print('\n'.join(selected))


if __name__ == '__main__':
    main()
