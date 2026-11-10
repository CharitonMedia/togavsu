// Files tab: resumable chunked uploads through the files API, and small POST actions.
(function () {
  "use strict";
  var csrf = (document.querySelector('meta[name="csrf-token"]') || {}).content || "";

  function api(method, url, body, headers) {
    var h = Object.assign({"X-CSRF-Token": csrf}, headers || {});
    if (body !== undefined && !(body instanceof Blob)) {
      h["Content-Type"] = "application/json";
      body = JSON.stringify(body);
    }
    return fetch(url, {method: method, headers: h, body: body, credentials: "same-origin"}).then(function (r) {
      return r.text().then(function (t) {
        var data = t ? JSON.parse(t) : {};
        if (!r.ok) { var e = new Error(data.message || r.statusText); e.status = r.status; throw e; }
        return data;
      });
    });
  }

  function wait(ms) { return new Promise(function (ok) { setTimeout(ok, ms); }); }

  // sendFrom sends chunks from offset. A failed chunk asks the server where to resume.
  function sendFrom(file, up, offset, tries, report) {
    if (offset >= file.size) { return Promise.resolve(); }
    var end = Math.min(offset + up.chunk_size_bytes, file.size);
    var range = "bytes " + offset + "-" + (end - 1) + "/" + file.size;
    report(offset, file.size);
    return api("PUT", "/api/v1/file-uploads/" + up.id, file.slice(offset, end), {"Content-Range": range, "Content-Type": "application/octet-stream"})
      .then(function (u) { return sendFrom(file, up, u.next_offset, 0, report); })
      .catch(function (err) {
        if (tries >= 8) { throw err; }
        return wait(Math.min(30000, 1000 * Math.pow(2, tries)))
          .then(function () { return api("GET", "/api/v1/file-uploads/" + up.id); })
          .then(function (u) { return sendFrom(file, up, u.next_offset, tries + 1, report); },
                function () { return sendFrom(file, up, offset, tries + 1, report); });
      });
  }

  function upload(file, box, list) {
    var li = document.createElement("li");
    li.textContent = file.name + ": starting";
    list.appendChild(li);
    var report = function (done, total) { li.textContent = file.name + ": " + Math.floor(100 * done / Math.max(total, 1)) + "%"; };
    var body = {file_name: file.name, size_bytes: file.size, media_type: file.type || "application/octet-stream"};
    if (box.dataset.matter) { body.matter_id = box.dataset.matter; }
    return api("POST", "/api/v1/file-uploads", body).then(function (up) {
      return sendFrom(file, up, up.next_offset, 0, report).then(function () {
        return api("POST", "/api/v1/file-uploads/" + up.id + "/complete");
      }).then(function () {
        var pub = {upload_id: up.id};
        if (box.dataset.folder) { pub.folder_id = box.dataset.folder; }
        return api("POST", "/api/v1/files", pub);
      }).then(function (f) {
        li.textContent = f.name + ": uploaded" + (f.warnings && f.warnings.length ? " (a file with the same contents is already on this matter)" : "");
      });
    }).catch(function (err) {
      li.className = "failed";
      li.textContent = file.name + ": " + err.message;
    });
  }

  var box = document.getElementById("upload");
  if (box) {
    var pick = document.getElementById("pick"), list = document.getElementById("progress"), drop = box.querySelector(".drop");
    var run = function (files) {
      var chain = Promise.resolve();
      Array.prototype.forEach.call(files, function (f) { chain = chain.then(function () { return upload(f, box, list); }); });
      chain.then(function () { window.location.reload(); });
    };
    pick.addEventListener("change", function () { run(pick.files); });
    ["dragenter", "dragover"].forEach(function (n) { drop.addEventListener(n, function (e) { e.preventDefault(); drop.classList.add("over"); }); });
    ["dragleave", "drop"].forEach(function (n) { drop.addEventListener(n, function (e) { e.preventDefault(); drop.classList.remove("over"); }); });
    drop.addEventListener("drop", function (e) { run(e.dataTransfer.files); });
  }

  document.addEventListener("click", function (e) {
    var el = e.target.closest("[data-confirm]");
    if (el && !window.confirm(el.dataset.confirm)) { e.preventDefault(); return; }
    var post = e.target.closest("[data-post]");
    if (!post) { return; }
    e.preventDefault();
    api("POST", post.dataset.post, {}).then(function () { window.location.reload(); }, function (err) { window.alert(err.message); });
  });

  var move = document.getElementById("move-form");
  if (move) {
    move.addEventListener("submit", function (e) {
      e.preventDefault();
      var f = new FormData(move);
      api("POST", "/api/v1/storage-moves", {from_backend_id: f.get("from_backend_id"), to_backend_id: f.get("to_backend_id"), scope: "all"})
        .then(function () { window.location.reload(); }, function (err) { window.alert(err.message); });
    });
  }
})();
