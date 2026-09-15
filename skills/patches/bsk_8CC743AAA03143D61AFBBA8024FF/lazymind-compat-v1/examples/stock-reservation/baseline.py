def reserve_stock(stock, requests):
    reserved = {}
    rejected = {}
    for request in requests:
        sku = request["sku"]
        quantity = request["quantity"]
        if stock.get(sku, 0) >= quantity:
            stock[sku] = stock.get(sku, 0) - quantity
            reserved[sku] = reserved.get(sku, 0) + quantity
        else:
            rejected[sku] = rejected.get(sku, 0) + quantity
    return reserved, rejected
