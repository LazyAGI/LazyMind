"""Crop a screenshot to the actual photo before image editing."""

import argparse
from pathlib import Path

try:
    from PIL import Image, ImageOps
except ImportError as exc:
    raise SystemExit(
        "crop_image.py requires Pillow. Install it with: python3 -m pip install Pillow"
    ) from exc


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    for edge in ("left", "top", "right", "bottom"):
        parser.add_argument(f"--{edge}", type=int, required=True)
    args = parser.parse_args()

    with Image.open(args.input) as source:
        image = ImageOps.exif_transpose(source)
        width, height = image.size
        box = (args.left, args.top, args.right, args.bottom)
        if not (0 <= args.left < args.right <= width and 0 <= args.top < args.bottom <= height):
            parser.error(f"crop box {box} is outside image bounds {(width, height)}")
        cropped = image.crop(box)
        args.output.parent.mkdir(parents=True, exist_ok=True)
        if args.output.suffix.lower() in {".jpg", ".jpeg"}:
            cropped.convert("RGB").save(args.output, quality=95)
        elif args.output.suffix.lower() == ".png":
            cropped.save(args.output)
        else:
            parser.error("output must end with .jpg, .jpeg, or .png")
        print(f"{args.output} {cropped.width}x{cropped.height}")


if __name__ == "__main__":
    main()
