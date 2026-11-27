#!/usr/bin/env python3
"""Read-only Deploy status UI regression; every HTTP request is intercepted.
Run from repository root with Playwright and installed Chromium.
No live controller, SSH connection, deployment or service mutation.
"""
import os
from pathlib import Path
from playwright.sync_api import sync_playwright
import json
from urllib.parse import urlparse
html=Path("adapt/csqtt-panel-preview.html").read_text()
url="http://fixture.invalid/panel"
state={"status":{"deploying":False,"result":"idle","mode":"install"},"code":200}
writes=[]
defaults={"/configs":[],"/configs/selected":{},"/settings":{},"/vpn/status":{"connected":False},"/tunnel/auth":{"configured":False},"/vk/state":{"hasToken":False},"/captcha/state":{"pending":False},"/version":{"backend":"fixture","ui":"fixture"},"/deploy/log":"fixture log"}
def route(r):
    req=r.request
    path=urlparse(req.url).path
    if req.method!="GET":
        writes.append((req.method,path))
        r.fulfill(status=403,body="fixture refuses mutation")
        return
    if req.url==url:
        r.fulfill(content_type="text/html",body=html);return
    if not req.url.startswith("http://fixture.invalid/api/"):
        r.abort();return
    key=path.removeprefix("/api")
    if key=="/deploy/status":
        status=state["code"]
        body=state["status"] if status==200 else {"error":"fixture unavailable"}
    else:
        status=200
        body=defaults.get(key,{})
    r.fulfill(status=status,content_type="application/json",body=json.dumps(body))
with sync_playwright() as pw:
    browser=pw.chromium.launch(headless=True,**({"executable_path":os.environ["CSQTT_TEST_CHROMIUM"]} if os.environ.get("CSQTT_TEST_CHROMIUM") else {}))
    page=browser.new_page()
    page.route("**/*",route)
    page.goto(url,wait_until="domcontentloaded")
    page.wait_for_function("document.querySelector('#apiStatus').textContent.includes('API онлайн')")
    page.locator('button[data-page="deploy"]').click()
    def open_progress():
        page.locator("#refreshDeployStatus").click()
        page.locator("#deployStatusOverlay").wait_for(state="visible")
        page.wait_for_function("!document.querySelector('#deployStatusRetry').disabled")
        page.locator("#deployStatusViewProgress").click()
        page.locator("#deployProgressOverlay").wait_for(state="visible")
    def open_and_poll(status):
        state.update(status=status,code=200)
        open_progress()
    def finish(title,kind):
        page.wait_for_function("(t) => document.querySelector('#deployProgressPhase').textContent===t",arg=title,timeout=11000)
        assert page.locator("#deployProgressOutcome").get_attribute("data-kind")==kind
        assert page.locator("#deployProgressIndicator").get_attribute("data-active")=="false"
        page.locator("#deployProgressClose").click()
    open_and_poll({"deploying":False,"result":"completed"})
    finish("Команда завершена","neutral")
    print("PASS completed without confirmed installation remains neutral",flush=True)
    open_and_poll({"deploying":True,"result":"running","progress":60,"stage":"fixture"})
    page.wait_for_function("document.querySelector('#deployProgressOutcome').textContent.includes('60%')")
    state["status"]={"deploying":False,"result":"unknown"}
    finish("Результат неизвестен","error")
    print("PASS running then missing terminal result remains unconfirmed",flush=True)
    state["code"]=503
    open_progress()
    page.wait_for_function("document.querySelector('#deployProgressPhase').textContent==='Связь с API прервана'")
    state.update(code=200,status={"deploying":False,"result":"completed"})
    finish("Команда завершена","neutral")
    print("PASS transient HTTP503 recovers without a false success",flush=True)
    state["code"]=503
    open_progress()
    finish("Результат неизвестен","error")
    print("PASS persistent HTTP503 terminates with unknown outcome",flush=True)
    assert not writes,writes
    print("PASS zero mutating HTTP requests; all network routes intercepted",flush=True)
    browser.close()
