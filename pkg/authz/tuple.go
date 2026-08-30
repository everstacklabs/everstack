package authz

import (
	"fmt"
	"strings"
)

// Object identifies a thing access is granted on, e.g. organization:acme,
// workspace:prod, dataset:42. Type is one of the schema type names.
type Object struct {
	Type string
	ID   string
}

// String renders the object as "type:id".
func (o Object) String() string { return o.Type + ":" + o.ID }

// IsZero reports whether the object is unset.
func (o Object) IsZero() bool { return o.Type == "" && o.ID == "" }

// ParseObject parses "type:id". The id may itself contain ':' (e.g. a URL),
// so only the first ':' is the separator.
func ParseObject(s string) (Object, error) {
	i := strings.IndexByte(s, ':')
	if i <= 0 || i == len(s)-1 {
		return Object{}, fmt.Errorf("authz: invalid object %q (want type:id)", s)
	}
	return Object{Type: s[:i], ID: s[i+1:]}, nil
}

// UserType is the reserved type name for a concrete principal (a user id).
const UserType = "user"

// Subject is who a tuple grants access to. It is either a concrete user
// (Relation == "") or a userset — every subject that has Relation on the
// referenced Object (e.g. organization:acme#member means "all members of
// org acme").
type Subject struct {
	Object   Object // for a user: {Type: "user", ID: <user id>}
	Relation string // "" for a concrete user; otherwise a userset relation
}

// User builds a concrete user subject.
func User(id string) Subject { return Subject{Object: Object{Type: UserType, ID: id}} }

// UsersetSubject builds a userset subject: obj#relation (e.g. every member of
// an org). Named to avoid colliding with the Userset rewrite type in schema.go.
func UsersetSubject(obj Object, relation string) Subject {
	return Subject{Object: obj, Relation: relation}
}

// IsUser reports whether the subject is a concrete user (not a userset).
func (s Subject) IsUser() bool { return s.Relation == "" && s.Object.Type == UserType }

// String renders the subject as "type:id" or "type:id#relation".
func (s Subject) String() string {
	if s.Relation == "" {
		return s.Object.String()
	}
	return s.Object.String() + "#" + s.Relation
}

// ParseSubject parses "type:id" or "type:id#relation".
func ParseSubject(s string) (Subject, error) {
	rel := ""
	if i := strings.LastIndexByte(s, '#'); i >= 0 {
		rel = s[i+1:]
		s = s[:i]
	}
	obj, err := ParseObject(s)
	if err != nil {
		return Subject{}, err
	}
	return Subject{Object: obj, Relation: rel}, nil
}

// Tuple is a single relationship: Subject has Relation on Object.
// Rendered canonically as "object#relation@subject".
type Tuple struct {
	Object   Object
	Relation string
	Subject  Subject
}

// String renders the tuple as "object#relation@subject".
func (t Tuple) String() string {
	return t.Object.String() + "#" + t.Relation + "@" + t.Subject.String()
}

// NewTuple is a convenience constructor.
func NewTuple(obj Object, relation string, subject Subject) Tuple {
	return Tuple{Object: obj, Relation: relation, Subject: subject}
}

// ParseTuple parses "object#relation@subject".
func ParseTuple(s string) (Tuple, error) {
	at := strings.LastIndexByte(s, '@')
	if at < 0 {
		return Tuple{}, fmt.Errorf("authz: invalid tuple %q (missing @subject)", s)
	}
	left, subjStr := s[:at], s[at+1:]
	hash := strings.LastIndexByte(left, '#')
	if hash < 0 {
		return Tuple{}, fmt.Errorf("authz: invalid tuple %q (missing #relation)", s)
	}
	objStr, rel := left[:hash], left[hash+1:]
	obj, err := ParseObject(objStr)
	if err != nil {
		return Tuple{}, err
	}
	subj, err := ParseSubject(subjStr)
	if err != nil {
		return Tuple{}, err
	}
	if rel == "" {
		return Tuple{}, fmt.Errorf("authz: invalid tuple %q (empty relation)", s)
	}
	return Tuple{Object: obj, Relation: rel, Subject: subj}, nil
}
