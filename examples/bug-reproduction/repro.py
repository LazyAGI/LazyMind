def discount(total, percent):
    return total * (1 - percent)


assert discount(100, 20) == 80, f"expected 80, actual {discount(100, 20)}"
