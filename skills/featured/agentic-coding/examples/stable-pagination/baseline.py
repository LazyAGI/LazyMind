def sorted_tickets(tickets):
    tickets.sort(key=lambda ticket: ticket["priority"])
    return tickets
