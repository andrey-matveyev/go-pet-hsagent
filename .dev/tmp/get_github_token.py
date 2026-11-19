import time
import jwt
import requests

APP_ID_PATH = "/home/andrey/projects/ai-assistants/ai-dev-assistant-bot-app-id"
PRIVATE_KEY_PATH = "/home/andrey/projects/ai-assistants/ai-dev-assistant-bot.2026-11-13.private-key.pem"
OWNER = "andrey-matveyev"
REPO = "go-pet-hsagent"

def get_app_id():
    with open(APP_ID_PATH, 'r') as f:
        content = f.read().strip()
        # Extract only digits in case the file contains "App ID: 12345"
        import re
        match = re.search(r'\d+', content)
        if match:
            return match.group()
        return content

def generate_jwt():
    with open(PRIVATE_KEY_PATH, 'r') as f:
        private_key = f.read()
    
    payload = {
        "iat": int(time.time()) - 60,
        "exp": int(time.time()) + (10 * 60),
        "iss": get_app_id(),
    }
    
    encoded_jwt = jwt.encode(payload, private_key, algorithm="RS256")
    return encoded_jwt

def get_installation_id(jwt_token):
    url = f"https://api.github.com/repos/{OWNER}/{REPO}/installation"
    headers = {
        "Authorization": f"Bearer {jwt_token}",
        "Accept": "application/vnd.github+json"
    }
    response = requests.get(url, headers=headers)
    response.raise_for_status()
    return response.json()["id"]

def get_access_token(jwt_token, installation_id):
    url = f"https://api.github.com/app/installations/{installation_id}/access_tokens"
    headers = {
        "Authorization": f"Bearer {jwt_token}",
        "Accept": "application/vnd.github+json"
    }
    response = requests.post(url, headers=headers)
    response.raise_for_status()
    return response.json()["token"]

if __name__ == "__main__":
    try:
        jwt_token = generate_jwt()
        inst_id = get_installation_id(jwt_token)
        access_token = get_access_token(jwt_token, inst_id)
        print(access_token)
    except Exception as e:
        print(f"Error: {e}")
