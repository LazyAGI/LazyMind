import json
import tempfile
import unittest
from pathlib import Path
import fitz
import main

class PreservationTests(unittest.TestCase):
    def render(self, root, blocks, translations):
        (root/'layout.json').write_text(json.dumps({'blocks':blocks}))
        (root/'translations.json').write_text(json.dumps(translations))
        return main.render_pdf_translation(main.PDFTranslationRenderRequest(
            source_path=str(root/'source.pdf'),layout_path=str(root/'layout.json'),
            translations_path=str(root/'translations.json'),output_path=str(root/'output.pdf')))

    def test_one_overflow_preserves_columns_formula_image_and_table(self):
        with tempfile.TemporaryDirectory(dir='/var/lib/lazymind/uploads') as directory:
            root=Path(directory)
            with fitz.open() as doc:
                p=doc.new_page(width=595,height=842)
                p.insert_text((50,100),'Left paragraph',fontsize=11)
                p.insert_text((320,100),'Right paragraph',fontsize=11)
                p.insert_text((50,200),'E = mc2',fontsize=12)
                p.draw_rect(fitz.Rect(50,250,250,320),color=(0,0,0))
                p.draw_line((50,285),(250,285),color=(0,0,0))
                p.insert_text((60,275),'Table cell',fontsize=10)
                p.insert_image(fitz.Rect(320,250,450,340),filename='/app/assets/lazymind-logo.png')
                p.insert_text((50,410),'Overflow',fontsize=10)
                doc.save(root/'source.pdf')
            blocks=[{'id':key,'page':1,'bbox':box,'text':text,'fontSize':11} for key,box,text in [
                ('left',[50,85,250,135],'Left paragraph'),
                ('right',[320,85,520,135],'Right paragraph'),
                ('overflow',[50,398,95,413],'Overflow'),
            ]]
            result=self.render(root,blocks,{'left':'左栏译文。','right':'右栏译文。','overflow':'完整长段译文。'*700+'末句不能丢。'})
            with fitz.open(root/'source.pdf') as original, fitz.open(root/'output.pdf') as translated:
                self.assertIn('E = mc2',translated[0].get_text())
                self.assertIn('Overflow',translated[0].get_text())
                self.assertIn('左栏译文',translated[0].get_text(clip=fitz.Rect(45,80,255,145)))
                self.assertIn('右栏译文',translated[0].get_text(clip=fitz.Rect(315,80,525,145)))
                for box in [fitz.Rect(45,180,150,210),fitz.Rect(45,245,255,325),fitz.Rect(315,245,455,345)]:
                    self.assertEqual(original[0].get_pixmap(clip=box).samples,translated[0].get_pixmap(clip=box).samples)
                self.assertIn('末句不能丢。',''.join(p.get_text() for p in translated).replace('\n',''))
                self.assertGreater(len(translated),1)
                self.assertTrue(any(link['kind']==fitz.LINK_GOTO for link in translated[0].get_links()))
                self.assertEqual(result['supplemental_block_count'],1)

    def test_inline_formula_and_table_content_are_preserved_when_unsafe(self):
        with tempfile.TemporaryDirectory(dir='/var/lib/lazymind/uploads') as directory:
            root=Path(directory)
            with fitz.open() as doc:
                p=doc.new_page()
                p.insert_text((50,100),'Energy E = mc2 remains exact.',fontsize=11)
                p.draw_rect(fitz.Rect(50,150,250,200),color=(0,0,0))
                p.insert_text((60,175),'Table value 42.5',fontsize=11)
                doc.save(root/'source.pdf')
            blocks=[{'id':key,'page':1,'bbox':box,'text':text,'fontSize':11} for key,box,text in [
                ('math',[50,84,300,110],'Energy E = mc2 remains exact.'),
                ('cell',[50,150,250,200],'Table value 42.5')]]
            result=self.render(root,blocks,{'math':'能量公式保持原样。','cell':'表格数值 42.5'})
            with fitz.open(root/'output.pdf') as output:
                self.assertIn('Energy E = mc2 remains exact.',output[0].get_text())
                self.assertIn('Table value 42.5',output[0].get_text())
                self.assertEqual(result['supplemental_block_count'],2)
                self.assertTrue(all(item['pages'] for item in result['supplements']))

    def test_rotated_margin_text_keeps_its_orientation(self):
        with tempfile.TemporaryDirectory(dir='/var/lib/lazymind/uploads') as directory:
            root=Path(directory)
            with fitz.open() as doc:
                page=doc.new_page()
                page.insert_text((25,400),'arXiv:1706.03762',fontsize=10,rotate=90)
                bbox=list(page.get_text('blocks')[0][:4])
                doc.save(root/'source.pdf')
            self.render(root,[{'id':'rotated','page':1,'bbox':bbox,'text':'arXiv:1706.03762','fontSize':10}],{'rotated':'预印本编号 1706.03762'})
            with fitz.open(root/'output.pdf') as output:
                lines=[line for block in output[0].get_text('dict')['blocks'] for line in block.get('lines',[])]
                rotated=[line for line in lines if any('arXiv' in span['text'] for span in line['spans'])]
                self.assertTrue(rotated)
                self.assertEqual(tuple(rotated[0]['dir']),(0.0,-1.0))

    def test_missing_translation_is_failure_not_silent_success(self):
        with tempfile.TemporaryDirectory(dir='/var/lib/lazymind/uploads') as directory:
            root=Path(directory)
            with fitz.open() as doc:
                doc.new_page();doc.save(root/'source.pdf')
            with self.assertRaises(Exception):
                self.render(root,[{'id':'present','page':1,'bbox':[50,50,250,150],'text':'Present paragraph'},
                                  {'id':'missing','page':1,'bbox':[50,180,250,280],'text':'Missing paragraph'}],{'present':'存在译文'})

if __name__=='__main__': unittest.main()
