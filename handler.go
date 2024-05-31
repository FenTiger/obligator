package obligator

import (
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"

	"github.com/lestrrat-go/jwx/v2/jwk"
)

type Handler struct {
	mux *http.ServeMux
}

func NewHandler(storage Storage, conf ServerConfig, tmpl *template.Template) *Handler {

	mux := http.NewServeMux()

	h := &Handler{
		mux: mux,
	}

	publicJwks, err := jwk.PublicSetOf(storage.GetJWKSet())
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	mux.Handle("/", http.FileServer(http.Dir("static")))

	mux.HandleFunc("/ip", func(w http.ResponseWriter, r *http.Request) {
		remoteIp, err := getRemoteIp(r, conf.BehindProxy)
		if err != nil {
			w.WriteHeader(500)
			io.WriteString(w, err.Error())
			return
		}

		data := struct {
			RootUri     string
			DisplayName string
			RemoteIp    string
			ReturnUri   string
		}{
			RootUri:     storage.GetRootUri(),
			DisplayName: storage.GetDisplayName(),
			RemoteIp:    remoteIp,
			ReturnUri:   "/login",
		}

		err = tmpl.ExecuteTemplate(w, "ip.html", data)
		if err != nil {
			w.WriteHeader(500)
			io.WriteString(w, err.Error())
			return
		}
	})

	// TODO: probably needs to be combined with the API somehow, but the
	// API currently only works over a unix socket for security.
	mux.HandleFunc("/validate", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()

		redirectUri := r.Form.Get("redirect_uri")
		url := fmt.Sprintf("%s/auth?client_id=%s&redirect_uri=%s&response_type=code&state=&scope=",
			storage.GetRootUri(), redirectUri, redirectUri)

		validation, err := validate(storage, r)
		if err != nil {
			fmt.Println(err)
			http.Redirect(w, r, url, 307)
			return
		}

		if validation != nil {
			w.Header().Set("Remote-Id-Type", validation.IdType)
			w.Header().Set("Remote-Id", validation.Id)
		} else {
			w.Header().Set("Remote-Id-Type", "")
			w.Header().Set("Remote-Id", "")
		}
	})

	loginFunc := func(w http.ResponseWriter, r *http.Request, fedCm bool) {

		idents, _ := getIdentities(storage, r, publicJwks)

		canEmail := true
		if _, err := storage.GetSmtpConfig(); err != nil {
			canEmail = false
		}

		providers, err := storage.GetOAuth2Providers()
		if err != nil {
			w.WriteHeader(500)
			io.WriteString(w, err.Error())
			return
		}

		returnUri := "/login"
		if fedCm {
			returnUri = "/login-fedcm-auto"
		}

		setReturnUriCookie(storage, returnUri, w)

		data := struct {
			DisplayName     string
			CanEmail        bool
			Identities      []*Identity
			OAuth2Providers []OAuth2Provider
			LogoMap         map[string]template.HTML
			ReturnUri       string
			RootUri         string
			FedCm           bool
		}{
			DisplayName:     storage.GetDisplayName(),
			CanEmail:        canEmail,
			Identities:      idents,
			OAuth2Providers: providers,
			LogoMap:         providerLogoMap,
			ReturnUri:       returnUri,
			RootUri:         storage.GetRootUri(),
			FedCm:           fedCm,
		}

		err = tmpl.ExecuteTemplate(w, "login.html", data)
		if err != nil {
			w.WriteHeader(500)
			io.WriteString(w, err.Error())
			return
		}
	}

	mux.HandleFunc("/login-fedcm-auto", func(w http.ResponseWriter, r *http.Request) {
		loginFunc(w, r, true)
	})
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		loginFunc(w, r, false)
	})

	mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {

		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}

		r.ParseForm()

		redirect := r.Form.Get("prev_page")

		err = deleteLoginKeyCookie(storage, w)
		if err != nil {
			w.WriteHeader(500)
			fmt.Fprintf(os.Stderr, err.Error())
		}

		w.Header().Add("Set-Login", "logged-out")
		http.Redirect(w, r, redirect, http.StatusSeeOther)
	})

	mux.HandleFunc("/no-account", func(w http.ResponseWriter, r *http.Request) {

		idents, _ := getIdentities(storage, r, publicJwks)

		data := struct {
			Identities  []*Identity
			URL         string
			RootUri     string
			DisplayName string
			ReturnUri   string
		}{
			Identities:  idents,
			URL:         fmt.Sprintf("/auth?%s", r.URL.RawQuery),
			RootUri:     storage.GetRootUri(),
			DisplayName: storage.GetDisplayName(),
			ReturnUri:   "/login",
		}

		err = tmpl.ExecuteTemplate(w, "no-account.html", data)
		if err != nil {
			w.WriteHeader(500)
			io.WriteString(w, err.Error())
			return
		}
	})

	mux.HandleFunc("/debug", func(w http.ResponseWriter, r *http.Request) {
		printJson(r.Header)
	})

	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}
