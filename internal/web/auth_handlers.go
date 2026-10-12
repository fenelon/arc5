package web

import (
	"errors"
	"fmt"
	"math"
	"net/http"

	"github.com/fenelon/arc5/internal/archive"
)

// formPage is the login or first-run page.
type formPage struct {
	Page
	Error string
}

func (s *Server) formPage(r *http.Request, title, problem string) formPage {
	return formPage{Page: Page{Title: title, Theme: theme(r)}, Error: problem}
}

// setupOpen reports whether first-run setup may still choose the password.
func (s *Server) setupOpen() (bool, error) {
	has, err := s.auth.hasPassword()
	return !has, err
}

func (s *Server) setupForm(w http.ResponseWriter, r *http.Request) {
	open, err := s.setupOpen()
	if err != nil {
		s.fail(w, err)
		return
	}
	if !open {
		redirect(w, r, "/login")
		return
	}
	s.render(w, http.StatusOK, "setup", s.formPage(r, "Choose a password", ""))
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	open, err := s.setupOpen()
	if err != nil {
		s.fail(w, err)
		return
	}
	if !open {
		redirect(w, r, "/login")
		return
	}
	password := r.PostFormValue("password")
	problem := ""
	if password != r.PostFormValue("password_again") {
		problem = "The two passwords are not the same. Type the same one twice."
	} else if err := s.auth.setPassword(password); errors.Is(err, errShortPassword) {
		problem = "That password is too short. Use at least 8 characters."
	} else if err != nil {
		s.fail(w, err)
		return
	}
	if problem != "" {
		s.render(w, http.StatusUnprocessableEntity, "setup", s.formPage(r, "Choose a password", problem))
		return
	}
	if err := s.auth.startSession(w, r); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, "/")
}

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	has, err := s.auth.hasPassword()
	if err != nil {
		s.fail(w, err)
		return
	}
	if !has {
		redirect(w, r, "/setup")
		return
	}
	if _, ok := s.auth.session(r); ok {
		redirect(w, r, "/")
		return
	}
	s.render(w, http.StatusOK, "login", s.formPage(r, "Sign in", ""))
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	has, err := s.auth.hasPassword()
	if err != nil {
		s.fail(w, err)
		return
	}
	if !has {
		redirect(w, r, "/setup")
		return
	}
	if wait := s.auth.wait(); wait > 0 {
		seconds := int(math.Ceil(wait.Seconds()))
		s.render(w, http.StatusTooManyRequests, "login", s.formPage(r, "Sign in",
			fmt.Sprintf("Too many wrong passwords. Try again in %d seconds.", seconds)))
		return
	}
	ok, err := s.auth.check(r.PostFormValue("password"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if !ok {
		s.auth.failed()
		s.render(w, http.StatusUnauthorized, "login", s.formPage(r, "Sign in", "That password is not right."))
		return
	}
	s.auth.succeeded()
	if err := s.auth.startSession(w, r); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, "/")
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, _ archive.Session) {
	s.auth.endSession(w, r)
	redirect(w, r, "/login")
}
