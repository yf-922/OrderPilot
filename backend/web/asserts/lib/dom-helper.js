// ProductLib 是页面共享的小型前端工具库。
// 这样商品页和用户页都可以复用统一的 DOM 与请求方法。
window.ProductLib = {
	storageKey: "product_admin_token",

	getToken: function () {
		return window.sessionStorage.getItem(this.storageKey) || "";
	},

	setToken: function (token) {
		if (token) {
			window.sessionStorage.setItem(this.storageKey, token);
		} else {
			window.sessionStorage.removeItem(this.storageKey);
		}
		this.refreshAuthStatus();
	},

	refreshAuthStatus: function () {
		var status = document.getElementById("auth-status");
		var logout = document.getElementById("logout-btn");
		var loggedIn = !!this.getToken();
		if (status) {
			status.textContent = loggedIn ? "已登录" : "未登录";
			status.className = "auth-status " + (loggedIn ? "signed-in" : "");
		}
		if (logout) {
			logout.hidden = !loggedIn;
		}
	},
    // setValue 根据元素 id 设置表单值。
    setValue: function (id, value) {
        var node = document.getElementById(id);
        if (node) {
            node.value = value || "";
        }
    },

    // setMessage 统一设置提示文本和样式类型。
    setMessage: function (id, text, type) {
        var node = document.getElementById(id);
        if (!node) {
            return;
        }

        node.textContent = text || "";
        node.className = "form-message " + (type || "");
    },

    // requestJSON 基于 fetch 发送请求，并假设返回体是 JSON。
	requestJSON: function (url, options) {
		options = options || {};
		var headers = new Headers(options.headers || {});
		var token = this.getToken();
		if (token) {
			headers.set("Authorization", "Bearer " + token);
		}
		options.headers = headers;
		return fetch(url, options).then(function (response) {
			return response.text().then(function (body) {
				var result;
				try { result = body ? JSON.parse(body) : {}; } catch (_) { result = {success: false, message: "服务器返回格式错误"}; }
				result.httpStatus = response.status;
				if (response.status === 401) {
					window.ProductLib.setToken("");
				}
				return result;
			});
		});
	}
};

document.addEventListener("DOMContentLoaded", function () {
	window.ProductLib.refreshAuthStatus();
	var logout = document.getElementById("logout-btn");
	if (logout) {
		logout.addEventListener("click", function () {
			window.ProductLib.setToken("");
			window.location.href = "/user";
		});
	}
});
