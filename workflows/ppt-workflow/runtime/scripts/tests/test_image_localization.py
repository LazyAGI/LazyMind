import sys
from pathlib import Path

import pytest
from PIL import Image

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import run_stage


def test_approved_external_material_is_copied_and_tag_preserved(tmp_path):
    image = tmp_path / 'source image.png'
    Image.new('RGB', (20, 10)).save(image)
    deck = tmp_path / 'deck'
    html, count = run_stage._prepare_local_imgs(
        f'<figure><img src="{image}" alt="evidence"></figure>', deck, str(image))
    assert count == 1 and '<img ' in html and 'alt="evidence"' in html
    assert '../images/material-' in html
    assert next((deck / 'images').glob('*')).read_bytes() == image.read_bytes()


def test_missing_image_fails_instead_of_silently_removing_it(tmp_path):
    html = '<figure><img src="../images/missing.png"></figure>'
    with pytest.raises(ValueError, match='Image file is missing'):
        run_stage._prepare_local_imgs(html, tmp_path)
    assert '<img ' in html


def test_unapproved_external_file_is_not_copied(tmp_path):
    image = tmp_path / 'private.png'
    Image.new('RGB', (20, 10)).save(image)
    with pytest.raises(ValueError, match='not an approved input'):
        run_stage._prepare_local_imgs(f'<img src="{image}">', tmp_path / 'deck')


def test_embedded_and_remote_images_preserved(tmp_path):
    html = '<img src="data:image/png;base64,abc"><img src="https://example.org/a.png">'
    assert run_stage._prepare_local_imgs(html, tmp_path) == (html, 0)
