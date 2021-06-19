
let cacheButton = document.getElementById("cache");
let extensionText = document.getElementById("extensionText");

cacheButton.addEventListener("click", async () => {
  browser.tabs.query({currentWindow: true, active: true}).then((tabs) => {
    let url = tabs[0].url;
    let digestedUrl = btoa(url);
    let advertisedAddress = "knox"
    let knoxUrl = "http://" + advertisedAddress + "/c/" + digestedUrl;
    extensionText.innerHTML = "<center>caching...</center>";
    let reject = function(result) {
        console.log("Calling reject.");
        extensionText.innerHTML = "<center><a target=\"_blank\" href=\"" + knoxUrl + "\"><p style=\"color:red\">failed to cache</p></a></center>";
    };
    let cancelled = false;
    fetch(knoxUrl).then(r => {
        if (r.status == 200) {
            return r.text();
        } else {
            reject();
            cancelled = true;
        }
    }
    , reject).then(result => {
        if (!cancelled) {
            extensionText.innerHTML = "<center><a target=\"_blank\" href=\"" + knoxUrl + "\">cached</a></center>";
        }
    }, reject);
  });
});
