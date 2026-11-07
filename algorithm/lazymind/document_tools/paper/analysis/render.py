"""Export the authored analysis without appending host-generated result inventories."""


def render(analysis):
    return analysis['summary']
