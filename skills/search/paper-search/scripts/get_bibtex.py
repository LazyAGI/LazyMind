import sys
import urllib.request
import xml.etree.ElementTree as ET

def get_bibtex(arxiv_id):
    url = f"https://export.arxiv.org/api/query?id_list={arxiv_id}"
    try:
        req = urllib.request.Request(url, headers={'User-Agent': 'Mozilla/5.0'})
        with urllib.request.urlopen(req) as response:
            xml_data = response.read()
            
        ns = {'a': 'http://www.w3.org/2005/Atom', 'arxiv': 'http://arxiv.org/schemas/atom'}
        root = ET.fromstring(xml_data)
        entry = root.find('a:entry', ns)
        
        if entry:
            title = entry.find('a:title', ns).text.strip().replace('\n', ' ')
            authors = ' and '.join(a.find('a:name', ns).text for a in entry.findall('a:author', ns))
            year = entry.find('a:published', ns).text[:4]
            raw_id = entry.find('a:id', ns).text.strip().split('/abs/')[-1]
            
            cat_elem = entry.find('arxiv:primary_category', ns)
            primary = cat_elem.get('term') if cat_elem is not None else 'cs.LG'
            last_name = entry.find('a:author', ns).find('a:name', ns).text.split()[-1]
            
            print("STATUS: SUCCESS")
            print("--- BIBTEX START ---")
            print(f"@article{{{last_name}{year}_{raw_id.replace('.', '')},")
            print(f"  title     = {{{title}}},")
            print(f"  author    = {{{authors}}},")
            print(f"  year      = {{{year}}},")
            print(f"  eprint    = {{{raw_id}}},")
            print(f"  archivePrefix = {{arXiv}},")
            print(f"  primaryClass  = {{{primary}}},")
            print(f"  url       = {{https://arxiv.org/abs/{raw_id}}}")
            print("}")
            print("--- BIBTEX END ---")
        else:
            print(f"ERROR: Paper with ID {arxiv_id} not found.")
            
    except Exception as e:
        print(f"ERROR: Failed generating BibTeX. Details: {e}")

if __name__ == "__main__":
    if len(sys.argv) > 1:
        target_id = sys.argv[1]
        get_bibtex(target_id)
    else:
        print("ERROR: Missing arXiv ID argument.")