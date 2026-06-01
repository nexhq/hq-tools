import os
import json
import datetime
import requests
import concurrent.futures

ORG_NAME = "nexhq"
GITHUB_TOKEN = os.getenv("GITHUB_TOKEN")
API_URL = f"https://api.github.com/orgs/{ORG_NAME}/repos"

def fetch_public_repos():
    """Fetches all public repositories for the organization."""
    headers = {
        "Accept": "application/vnd.github.v3+json",
        "X-GitHub-Api-Version": "2022-11-28"
    }
    if GITHUB_TOKEN:
        headers["Authorization"] = f"Bearer {GITHUB_TOKEN}"

    repos = []
    page = 1
    while True:
        response = requests.get(f"{API_URL}?type=public&per_page=100&page={page}", headers=headers)
        if response.status_code != 200:
            print(f"Failed to fetch repositories: {response.status_code} - {response.text}")
            break
            
        data = response.json()
        if not data:
            break
            
        repos.extend(data)
        page += 1
        
    return repos

def fetch_nex_json(repo_name, default_branch):
    """Fetches the nex.json file from the default branch of the given repository."""
    url = f"https://raw.githubusercontent.com/{ORG_NAME}/{repo_name}/{default_branch}/nex.json"
    response = requests.get(url)
    
    if response.status_code == 200:
        try:
            return response.json()
        except json.JSONDecodeError:
            print(f"Warning: Failed to parse JSON for {repo_name}")
    elif response.status_code == 404:
        # Expected if the repo doesn't have a tool manifest
        pass
    else:
        print(f"Warning: Failed to fetch {url} (Status: {response.status_code})")
        
    return None

def process_repo(repo):
    """Worker function to process a single repository."""
    name = repo["name"]
    # Skip this repository itself to avoid recursive scanning of the central repo
    if name == "hq-tools":
        return None
        
    default_branch = repo.get("default_branch", "main")

    nex_manifest = fetch_nex_json(name, default_branch)
    if nex_manifest:
        return nex_manifest
    return None

def main():
    print(f"Fetching public repositories for '{ORG_NAME}'...")
    repos = fetch_public_repos()
    print(f"Found {len(repos)} public repositories.")

    tools = []
    
    # Process repositories concurrently to map them much faster
    print("Scanning repositories concurrently for nex.json manifests...")
    with concurrent.futures.ThreadPoolExecutor(max_workers=10) as executor:
        # Submit all tasks
        future_to_repo = {executor.submit(process_repo, repo): repo for repo in repos}
        
        # Gather results as they complete
        for future in concurrent.futures.as_completed(future_to_repo):
            manifest = future.result()
            if manifest:
                print(f"  [+] Found valid tool manifest: {manifest.get('name', 'Unknown')}")
                tools.append(manifest)

    # Sort tools by name for consistent payload ordering
    tools = sorted(tools, key=lambda x: x.get("name", ""))

    # Construct the final registry payload
    registry = {
        "$schema": "https://raw.githubusercontent.com/nexhq/hq-tools/main/schema/tools.schema.json",
        "lastUpdated": datetime.datetime.utcnow().isoformat() + "Z",
        "organization": ORG_NAME,
        "tools": tools
    }

    # Write the output file
    tools_file_path = os.path.join(os.path.dirname(__file__), "..", "tools.json")
    with open(tools_file_path, "w", encoding="utf-8") as f:
        json.dump(registry, f, indent=4)

    print(f"\nSuccess! Registry built with {len(tools)} tools.")
    print(f"Output saved to {os.path.abspath(tools_file_path)}")

if __name__ == "__main__":
    main()
