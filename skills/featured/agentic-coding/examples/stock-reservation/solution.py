def reserve_stock(stock, requests):
    remaining = stock.copy()
    reserved = {}
    rejected = {}
    for request in requests:
        sku = request["sku"]
        quantity = request["quantity"]
        if remaining.get(sku, 0) >= quantity:
            remaining[sku] = remaining.get(sku, 0) - quantity
            reserved[sku] = reserved.get(sku, 0) + quantity
        else:
            rejected[sku] = rejected.get(sku, 0) + quantity
    return reserved, rejected
