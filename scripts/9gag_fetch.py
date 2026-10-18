#!/usr/bin/env python3
"""Fetch a 9GAG post (the API's data.post, as is) using curl_cffi browser impersonation to bypass Cloudflare."""

import json
import sys

try:
    from curl_cffi import requests
except ImportError:
    print(json.dumps({"error": "curl_cffi not installed. Run: pip install curl_cffi"}), file=sys.stdout)
    sys.exit(1)


def fetch_post(post_id):
    """Fetch a 9GAG post by ID using browser impersonation."""
    try:
        r = requests.get(
            f"https://9gag.com/v1/post?id={post_id}",
            impersonate="chrome",
            timeout=15,
        )

        if r.status_code != 200:
            return {"error": f"HTTP {r.status_code}"}

        data = r.json()
        post = data.get("data", {}).get("post", {})

        if not post:
            return {"error": "Post not found in response"}

        # The post as the API returns it: images (photo and MP4 renditions),
        # and article (blocks and medias) for Article posts.
        return post

    except Exception as e:
        return {"error": str(e)}


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print(json.dumps({"error": "Usage: 9gag_fetch.py <post_id>"}))
        sys.exit(1)

    post_id = sys.argv[1]
    result = fetch_post(post_id)
    print(json.dumps(result))
