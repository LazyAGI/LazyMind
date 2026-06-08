import sys
import urllib.request
import urllib.parse
import xml.etree.ElementTree as ET

def search_arxiv(query, max_results=5):
    encoded_query = urllib.parse.quote(query).replace('%20', '+')
    url = f"https://export.arxiv.org/api/query?search_query={encoded_query}&max_results={max_results}&sortBy=submittedDate&sortOrder=descending"
    
    try:
        req = urllib.request.Request(url, headers={'User-Agent': 'Mozilla/5.0'})
        with urllib.request.urlopen(req) as response:
            xml_data = response.read()
            
        ns = {'a': 'http://www.w3.org/2005/Atom'}
        root = ET.fromstring(xml_data)
        entries = root.findall('a:entry', ns)
        
        if not entries:
            print("STATUS: NO_RESULTS")
            print(f"No papers found for query: '{query}'")
            return

        print(f"Search Results for '{query}':\n" + "="*40)
        for i, entry in enumerate(entries):
            title = entry.find('a:title', ns).text.strip().replace('\n', ' ')
            arxiv_id = entry.find('a:id', ns).text.strip().split('/abs/')[-1]
            published = entry.find('a:published', ns).text[:10]
            authors = ', '.join(a.find('a:name', ns).text for a in entry.findall('a:author', ns))
            summary = entry.find('a:summary', ns).text.strip().replace('\n', ' ')
            
            print(f"Result [{i+1}] ID: {arxiv_id}")
            print(f"Title: {title}")
            print(f"Authors: {authors}")
            print(f"Published: {published}")
            print(f"Abstract: {summary[:200]}...")
            print(f"URL: https://arxiv.org/abs/{arxiv_id}")
            print("-" * 40)
            
    except Exception as e:
        print(f"ERROR: Failed fetching data from arXiv. Details: {e}")

if __name__ == "__main__":
    if len(sys.argv) > 1:
        search_query = sys.argv[1]
        search_arxiv(search_query)
    else:
        print("ERROR: Missing search query argument.")