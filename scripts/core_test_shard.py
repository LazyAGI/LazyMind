"""Select one core CI shard from the packages printed by go list ./...."""
import argparse
import sys


PACKAGES = {
    'root': 'lazymind/core',
    'chat': 'lazymind/core/chat',
    'workflow': 'lazymind/core/workflow',
}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('shard', choices=[*PACKAGES, 'other'])
    args = parser.parse_args()
    packages = [line.strip() for line in sys.stdin if line.strip()]
    if args.shard == 'other':
        selected = [package for package in packages if package not in PACKAGES.values()]
    else:
        selected = [package for package in packages if package == PACKAGES[args.shard]]
    if not selected:
        parser.error(f'no packages selected for core shard {args.shard}')
    print('\n'.join(selected))


if __name__ == '__main__':
    main()
