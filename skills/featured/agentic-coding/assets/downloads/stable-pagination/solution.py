def sorted_tickets(tickets):
    return sorted(tickets, key=lambda ticket: ticket["priority"])


def paginate_tickets(tickets, page, page_size):
    for name, value in (("page", page), ("page_size", page_size)):
        if isinstance(value, bool) or not isinstance(value, int):
            raise TypeError(f"{name} must be an integer, not bool")
        if value <= 0:
            raise ValueError(f"{name} must be positive")
    start = (page - 1) * page_size
    return sorted_tickets(tickets)[start:start + page_size]
