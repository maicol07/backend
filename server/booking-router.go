package main

import (
	"errors"
	"log"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/gorilla/mux"
)

type BookingRouter struct {
}

type BookingRequest struct {
	Enter     time.Time `json:"enter" validate:"required"`
	Leave     time.Time `json:"leave" validate:"required"`
	UserEmail string    `json:"userEmail"`
}

type CreateBookingRequest struct {
	SpaceID string `json:"spaceId" validate:"required"`
	BookingRequest
}

type PreCreateBookingRequest struct {
	LocationID string `json:"locationID" validate:"required"`
	BookingRequest
}

type GetBookingResponse struct {
	ID        string           `json:"id"`
	UserID    string           `json:"userId"`
	UserEmail string           `json:"userEmail"`
	Space     GetSpaceResponse `json:"space"`
	CreateBookingRequest
}

type GetBookingFilterRequest struct {
	Start      time.Time `json:"start" validate:"required"`
	End        time.Time `json:"end" validate:"required"`
	LocationID string    `json:"locationId"`
}

type GetPresenceReportResult struct {
	Users     []GetUserInfoSmall `json:"users"`
	Dates     []string           `json:"dates"`
	Presences [][]int            `json:"presences"`
}

type DebugTimeIssuesRequest struct {
	Time time.Time `json:"time" validate:"required"`
}

type DebugTimeIssuesResponse struct {
	Timezone                string    `json:"tz"`
	Error                   string    `json:"error"`
	ReceivedTime            string    `json:"receivedTime"`
	ReceivedTimeTransformed string    `json:"receivedTimeTransformed"`
	Database                time.Time `json:"dbTime"`
	Result                  time.Time `json:"result"`
}

func (router *BookingRouter) setupRoutes(s *mux.Router) {
	s.HandleFunc("/debugtimeissues/", router.debugTimeIssues).Methods("POST")
	s.HandleFunc("/report/presence/", router.getPresenceReport).Methods("POST")
	s.HandleFunc("/filter/", router.getFiltered).Methods("POST")
	s.HandleFunc("/precheck/", router.preBookingCreateCheck).Methods("POST")
	s.HandleFunc("/{id}", router.getOne).Methods("GET")
	s.HandleFunc("/{id}", router.update).Methods("PUT")
	s.HandleFunc("/{id}", router.delete).Methods("DELETE")
	s.HandleFunc("/", router.create).Methods("POST")
	s.HandleFunc("/", router.getAll).Methods("GET")
}

func (router *BookingRouter) debugTimeIssues(w http.ResponseWriter, r *http.Request) {
	var m DebugTimeIssuesRequest
	if UnmarshalValidateBody(r, &m) != nil {
		SendBadRequest(w)
		return
	}
	tz := "America/Los_Angeles"
	res := &DebugTimeIssuesResponse{
		Timezone:     tz,
		ReceivedTime: m.Time.String(),
		Error:        "No error",
	}
	_, err := time.LoadLocation(tz)
	if err != nil {
		res.Error = "Could not load timezone: " + err.Error()
		SendJSON(w, res)
		return
	}
	timeNew, err := attachTimezoneInformationTz(m.Time, tz)
	if err != nil {
		res.Error = "Could not attach timezone information (incoming): " + err.Error()
		SendJSON(w, res)
		return
	}
	res.ReceivedTimeTransformed = timeNew.String()
	e := &DebugTimeIssueItem{
		Created: timeNew,
	}
	if err := GetDebugTimeIssuesRepository().Create(e); err != nil {
		res.Error = "Could not create database record: " + err.Error()
		SendJSON(w, res)
		return
	}
	defer GetDebugTimeIssuesRepository().Delete(e)
	e2, err := GetDebugTimeIssuesRepository().GetOne(e.ID)
	if err != nil {
		res.Error = "Could not load database record: " + err.Error()
		SendJSON(w, res)
		return
	}
	res.Database = e2.Created
	timeToSend, err := attachTimezoneInformationTz(e2.Created, tz)
	if err != nil {
		res.Error = "Could not attach timezone information (outgoing): " + err.Error()
		SendJSON(w, res)
		return
	}
	res.Result = timeToSend
	SendJSON(w, res)
}

func (router *BookingRouter) getFiltered(w http.ResponseWriter, r *http.Request) {
	user := GetRequestUser(r)
	if !CanSpaceAdminOrg(user, user.OrganizationID) {
		SendForbidden(w)
		return
	}
	var m GetBookingFilterRequest
	if UnmarshalValidateBody(r, &m) != nil {
		SendBadRequest(w)
		return
	}
	list, err := GetBookingRepository().GetAllByOrg(user.OrganizationID, m.Start, m.End)
	if err != nil {
		log.Println(err)
		SendInternalServerError(w)
		return
	}
	res := []*GetBookingResponse{}
	for _, e := range list {
		m := router.copyToRestModel(e)
		res = append(res, m)
	}
	SendJSON(w, res)
}

func (router *BookingRouter) getOne(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	e, err := GetBookingRepository().GetOne(vars["id"])
	if err != nil {
		log.Println(err)
		SendNotFound(w)
		return
	}
	requestUser := GetRequestUser(r)
	if !CanAccessOrg(requestUser, requestUser.OrganizationID) && e.UserID != GetRequestUserID(r) {
		SendForbidden(w)
		return
	}
	if e.UserID != GetRequestUserID(r) && !CanSpaceAdminOrg(requestUser, requestUser.OrganizationID) {
		SendForbidden(w)
		return
	}
	res := router.copyToRestModel(e)
	SendJSON(w, res)
}

func (router *BookingRouter) getAll(w http.ResponseWriter, r *http.Request) {
	list, err := GetBookingRepository().GetAllByUser(GetRequestUserID(r), time.Now().UTC())
	if err != nil {
		log.Println(err)
		SendInternalServerError(w)
		return
	}
	res := []*GetBookingResponse{}
	for _, e := range list {
		m := router.copyToRestModel(e)
		res = append(res, m)
	}
	SendJSON(w, res)
}

func (router *BookingRouter) update(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	e, err := GetBookingRepository().GetOne(vars["id"])
	if err != nil {
		SendNotFound(w)
		return
	}
	var m CreateBookingRequest
	if UnmarshalValidateBody(r, &m) != nil {
		SendBadRequest(w)
		return
	}
	space, err := GetSpaceRepository().GetOne(m.SpaceID)
	if err != nil {
		SendBadRequest(w)
		return
	}
	location, err := GetLocationRepository().GetOne(space.LocationID)
	if err != nil {
		SendBadRequest(w)
		return
	}
	requestUser := GetRequestUser(r)
	if e.UserID != requestUser.ID && !CanSpaceAdminOrg(requestUser, location.OrganizationID) {
		SendForbidden(w)
		return
	}
	eNew, err := router.copyFromRestModel(&m, location)
	if err != nil {
		SendInternalServerError(w)
		return
	}
	eNew.ID = e.ID
	eNew.UserID = e.UserID
	if m.UserEmail != "" && m.UserEmail != requestUser.Email {
		if !CanSpaceAdminOrg(requestUser, location.OrganizationID) {
			SendForbidden(w)
			return
		}
		eNew.UserID, err = router.bookForUser(requestUser, m.UserEmail, w)
		if err != nil {
			SendInternalServerError(w)
			return
		}
	}
	bookingReq := &BookingRequest{
		Enter: eNew.Enter,
		Leave: eNew.Leave,
	}
	if valid, code := router.checkBookingCreateUpdate(bookingReq, location, requestUser, eNew.ID); !valid {
		SendBadRequestCode(w, code)
		return
	}
	conflicts, err := GetBookingRepository().GetConflicts(eNew.SpaceID, eNew.Enter, eNew.Leave, eNew.ID)
	log.Println(conflicts)
	if err != nil {
		log.Println(err)
		SendInternalServerError(w)
		return
	}
	if len(conflicts) > 0 {
		SendAleadyExists(w)
		return
	}
	if valid, code := router.isValidBookingCreationUpdateCheckingTollerance(bookingReq, conflicts, location.OrganizationID); !valid {
		SendBadRequestCode(w, code)
		return
	}
	if err := GetBookingRepository().Update(eNew); err != nil {
		log.Println(err)
		SendInternalServerError(w)
		return
	}
	SendUpdated(w)
}

func (router *BookingRouter) delete(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	e, err := GetBookingRepository().GetOne(vars["id"])
	if err != nil {
		SendNotFound(w)
		return
	}
	space, err := GetSpaceRepository().GetOne(e.SpaceID)
	if err != nil {
		SendBadRequest(w)
		return
	}
	location, err := GetLocationRepository().GetOne(space.LocationID)
	if err != nil {
		SendBadRequest(w)
		return
	}
	if !CanAccessOrg(GetRequestUser(r), location.OrganizationID) {
		SendForbidden(w)
		return
	}
	if (e.UserID != GetRequestUserID(r)) && !CanSpaceAdminOrg(GetRequestUser(r), location.OrganizationID) {
		SendForbidden(w)
		return
	}
	if err := GetBookingRepository().Delete(e); err != nil {
		SendInternalServerError(w)
		return
	}
	SendUpdated(w)
}

func (router *BookingRouter) checkBookingCreateUpdate(m *BookingRequest, location *Location, requestUser *User, bookingID string) (bool, int) {
	if valid, code := router.isValidBookingRequest(m, requestUser, location.OrganizationID, bookingID); !valid {
		return false, code
	}
	if !router.isValidConcurrent(m, location, bookingID) {
		return false, ResponseCodeBookingLocationMaxConcurrent
	}
	return true, 0
}

func (router *BookingRouter) preBookingCreateCheck(w http.ResponseWriter, r *http.Request) {
	var m PreCreateBookingRequest
	if UnmarshalValidateBody(r, &m) != nil {
		SendBadRequest(w)
		return
	}
	location, err := GetLocationRepository().GetOne(m.LocationID)
	if err != nil {
		SendBadRequest(w)
		return
	}
	requestUser := GetRequestUser(r)
	if !CanAccessOrg(requestUser, location.OrganizationID) {
		SendForbidden(w)
		return
	}
	enterNew, err := attachTimezoneInformation(m.Enter, location)
	if err != nil {
		SendInternalServerError(w)
		return
	}
	leaveNew, err := attachTimezoneInformation(m.Leave, location)
	if err != nil {
		SendInternalServerError(w)
		return
	}
	bookingReq := &BookingRequest{
		Enter: enterNew,
		Leave: leaveNew,
	}
	if valid, code := router.checkBookingCreateUpdate(bookingReq, location, requestUser, ""); !valid {
		SendBadRequestCode(w, code)
		return
	}
	SendUpdated(w)
}

func (router *BookingRouter) create(w http.ResponseWriter, r *http.Request) {
	var m CreateBookingRequest
	if UnmarshalValidateBody(r, &m) != nil {
		SendBadRequest(w)
		return
	}
	space, err := GetSpaceRepository().GetOne(m.SpaceID)
	if err != nil {
		SendBadRequest(w)
		return
	}
	location, err := GetLocationRepository().GetOne(space.LocationID)
	if err != nil {
		SendBadRequest(w)
		return
	}
	requestUser := GetRequestUser(r)
	if !CanAccessOrg(requestUser, location.OrganizationID) {
		SendForbidden(w)
		return
	}
	e, err := router.copyFromRestModel(&m, location)
	if err != nil {
		SendInternalServerError(w)
		return
	}
	e.UserID = GetRequestUserID(r)
	if m.UserEmail != "" && m.UserEmail != requestUser.Email {
		if !CanSpaceAdminOrg(requestUser, location.OrganizationID) {
			SendForbidden(w)
			return
		}
		e.UserID, err = router.bookForUser(requestUser, m.UserEmail, w)
		if err != nil {
			SendInternalServerError(w)
			return
		}
	}
	bookingReq := &BookingRequest{
		Enter: e.Enter,
		Leave: e.Leave,
	}
	if valid, code := router.checkBookingCreateUpdate(bookingReq, location, requestUser, ""); !valid {
		log.Println(err)
		SendBadRequestCode(w, code)
		return
	}
	// Check for other bookings that overlap with the same time.
	conflicts, err := GetBookingRepository().GetConflicts(e.SpaceID, e.Enter, e.Leave, "")
	if err != nil {
		log.Println(err)
		SendInternalServerError(w)
		return
	}
	tollerance, err := GetSettingsRepository().GetBool(location.OrganizationID, SettingRemoveCheckForConflicts.Name)
	if err != nil {
		log.Println(err)
		SendInternalServerError(w)
		return
	}
	if tollerance {
		if valid, code := router.isValidBookingCreationUpdateCheckingTollerance(bookingReq, conflicts, location.OrganizationID); !valid {
			SendBadRequestCode(w, code)
			return
		}
	} else {
		if len(conflicts) > 0 {
			SendAleadyExists(w)
			return
		}
	}
	if err := GetBookingRepository().Create(e); err != nil {
		log.Println(err)
		SendInternalServerError(w)
		return
	}
	SendCreated(w, e.ID)
}

func (router *BookingRouter) bookForUser(requestUser *User, userEmail string, w http.ResponseWriter) (string, error) {
	if !CanSpaceAdminOrg(requestUser, requestUser.OrganizationID) {
		SendForbidden(w)
		return "", errors.New("Forbidden")
	}
	bookForUser, err := GetUserRepository().GetByEmail(userEmail)
	if bookForUser == nil || err != nil {
		org, err := GetOrganizationRepository().GetOne(requestUser.OrganizationID)
		if err != nil || org == nil {
			SendInternalServerError(w)
			return "", errors.New("InternalServerError")
		}
		if allowed, _ := GetSettingsRepository().GetBool(org.ID, SettingAllowBookingsNonExistingUsers.Name); !allowed {
			SendForbidden(w)
			return "", errors.New("Forbidden")
		}
		if !GetUserRepository().canCreateUser(org) {
			SendInternalServerError(w)
			return "", errors.New("InternalServerError")
		}
		if !GetOrganizationRepository().isValidEmailForOrg(userEmail, org) {
			SendBadRequest(w)
			return "", errors.New("BadRequest")
		}
		user := &User{
			Email:          userEmail,
			AtlassianID:    NullString(""),
			OrganizationID: org.ID,
			Role:           UserRoleUser,
		}
		err = GetUserRepository().Create(user)
		if err != nil {
			SendInternalServerError(w)
			return "", errors.New("InternalServerError")
		}
		bookForUser, err = GetUserRepository().GetByEmail(userEmail)
		if err != nil {
			SendInternalServerError(w)
			return "", errors.New("InternalServerError")
		}
	}

	if bookForUser == nil {
		SendNotFound(w)
		return "", errors.New("NotFound")
	}
	return bookForUser.ID, nil
}

func (router *BookingRouter) getPresenceReport(w http.ResponseWriter, r *http.Request) {
	user := GetRequestUser(r)
	if !CanSpaceAdminOrg(user, user.OrganizationID) {
		SendForbidden(w)
		return
	}
	var m GetBookingFilterRequest
	if UnmarshalValidateBody(r, &m) != nil {
		SendBadRequest(w)
		return
	}
	var location *Location = nil
	if m.LocationID != "" {
		location, _ = GetLocationRepository().GetOne(m.LocationID)
		if location == nil {
			SendNotFound(w)
			return
		}
		if !GetUserRepository().isSuperAdmin(user) && location.OrganizationID != user.OrganizationID {
			SendForbidden(w)
			return
		}
	}
	items, err := GetBookingRepository().GetPresenceReport(user.OrganizationID, location, m.Start, m.End, 1000, 0)
	if err != nil {
		log.Println(err)
		SendInternalServerError(w)
		return
	}
	numUsers := len(items)
	numDates := 0
	if numUsers > 0 {
		numDates = len(items[0].Presence)
	}
	res := &GetPresenceReportResult{
		Users:     make([]GetUserInfoSmall, numUsers),
		Dates:     make([]string, numDates),
		Presences: make([][]int, numUsers),
	}
	i := 0
	for date := range items[0].Presence {
		res.Dates[i] = date
		i++
	}
	sort.Strings(res.Dates)
	for i, item := range items {
		res.Users[i] = GetUserInfoSmall{
			UserID: item.User.ID,
			Email:  item.User.Email,
		}
		res.Presences[i] = make([]int, numDates)
		for j, date := range res.Dates {
			res.Presences[i][j] = item.Presence[date]
		}
	}
	SendJSON(w, res)
}

func (router *BookingRouter) isValidBookingDuration(m *BookingRequest, orgID string, user *User) bool {
	noAdminRestrictions, _ := GetSettingsRepository().GetBool(orgID, SettingNoAdminRestrictions.Name)
	if noAdminRestrictions && CanSpaceAdminOrg(user, orgID) {
		return true
	}
	dailyBasisBooking, _ := GetSettingsRepository().GetBool(orgID, SettingDailyBasisBooking.Name)
	maxDurationHours, _ := GetSettingsRepository().GetInt(orgID, SettingMaxBookingDurationHours.Name)
	if dailyBasisBooking && (maxDurationHours%24 != 0) {
		maxDurationHours += (24 - (maxDurationHours % 24))
	}

	// Due to daylight saving time, days can have more or less than 24 hours
	hoursOnDate := router.getHoursOnDate(&m.Leave)
	durationNotRounded := int(math.Round(m.Leave.Sub(m.Enter).Minutes()) / 60)
	if dailyBasisBooking {
		return durationNotRounded%hoursOnDate == 0
	}

	// For non-daily-basis bookings, check exact duration
	duration := math.Floor(m.Leave.Sub(m.Enter).Minutes()) / 60
	if duration < 0 || duration > float64(maxDurationHours) {
		return false
	}
	return true
}

func (router *BookingRouter) getHoursOnDate(t *time.Time) int {
	start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	end := time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, t.Location())
	durationNotRounded := int(math.Round(end.Sub(start).Minutes()) / 60)
	return durationNotRounded
}

func (router *BookingRouter) isValidBookingAdvance(m *BookingRequest, orgID string, user *User) bool {
	noAdminRestrictions, _ := GetSettingsRepository().GetBool(orgID, SettingNoAdminRestrictions.Name)
	maxAdvanceDays, _ := GetSettingsRepository().GetInt(orgID, SettingMaxDaysInAdvance.Name)
	dailyBasisBooking, _ := GetSettingsRepository().GetBool(orgID, SettingDailyBasisBooking.Name)
	// allow Enter-Date in past if at least this morning
	now := time.Now().UTC()
	now = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if dailyBasisBooking {
		now = now.Add(-12 * time.Hour)
	}
	if m.Leave.Before(now) { // Leave must not be in past
		return false
	}
	advanceDays := math.Floor(m.Enter.Sub(now).Hours() / 24)
	if advanceDays >= 0 && noAdminRestrictions && CanSpaceAdminOrg(user, orgID) {
		return true
	}
	if advanceDays < 0 || advanceDays > float64(maxAdvanceDays) {
		return false
	}
	return true
}

func (router *BookingRouter) isValidMaxUpcomingBookings(orgID string, user *User) bool {
	noAdminRestrictions, _ := GetSettingsRepository().GetBool(orgID, SettingNoAdminRestrictions.Name)
	if noAdminRestrictions && CanSpaceAdminOrg(user, orgID) {
		return true
	}
	maxUpcoming, _ := GetSettingsRepository().GetInt(orgID, SettingMaxBookingsPerUser.Name)
	curUpcoming, _ := GetBookingRepository().GetAllByUser(user.ID, time.Now().UTC())
	return len(curUpcoming) < maxUpcoming
}

func (router *BookingRouter) isValidMaxConcurrentBookingsForUser(orgID string, user *User, m *BookingRequest, bookingID string) bool {
	noAdminRestrictions, _ := GetSettingsRepository().GetBool(orgID, SettingNoAdminRestrictions.Name)
	if noAdminRestrictions && CanSpaceAdminOrg(user, orgID) {
		return true
	}
	maxConcurrent, _ := GetSettingsRepository().GetInt(orgID, SettingMaxConcurrentBookingsPerUser.Name)
	// 0 = no limit
	if maxConcurrent == 0 {
		return true
	}
	curAtTime, _ := GetBookingRepository().GetTimeRangeByUser(user.ID, m.Enter, m.Leave, bookingID)
	return len(curAtTime) < maxConcurrent
}

func (router *BookingRouter) isValidBookingRequest(m *BookingRequest, user *User, orgID string, bookingID string) (bool, int) {
	isUpdate := bookingID != ""
	if !router.isValidBookingDuration(m, orgID, user) {
		return false, ResponseCodeBookingInvalidBookingDuration
	}
	if !router.isValidBookingAdvance(m, orgID, user) {
		return false, ResponseCodeBookingTooManyDaysInAdvance
	}
	if !router.isValidMaxConcurrentBookingsForUser(orgID, user, m, bookingID) {
		return false, ResponseCodeBookingMaxConcurrentForUser
	}
	if !isUpdate {
		if !router.isValidMaxUpcomingBookings(orgID, user) {
			return false, ResponseCodeBookingTooManyUpcomingBookings
		}
	}
	return true, 0
}

func (router *BookingRouter) isValidConcurrent(m *BookingRequest, location *Location, bookingID string) bool {
	tollerance, err := GetSettingsRepository().GetBool(location.OrganizationID, SettingRemoveCheckForConflicts.Name)
	if err != nil {
		log.Println(err)
		return false
	}
	if !tollerance {
		return true
	}
	if location.MaxConcurrentBookings == 0 {
		return true
	}
	bookings, err := GetBookingRepository().GetConcurrent(location, m.Enter, m.Leave, bookingID)
	if err != nil {
		log.Println(err)
		return false
	}
	if bookings >= int(location.MaxConcurrentBookings) {
		return false
	}
	return true
}

func (router *BookingRouter) isValidBookingCreationUpdateCheckingTollerance(m *BookingRequest, conflicts []*Booking, orgId string) (bool, int) {
	tollerance, err := GetSettingsRepository().GetBool(orgId, SettingRemoveCheckForConflicts.Name)
	if err != nil {
		log.Println(err)
		return false, http.StatusInternalServerError
	}
	if !tollerance {
		// No checks need to be done.
		return true, 0
	}
	t1Time := time.Date(0, 1, 1, m.Enter.Hour(), m.Enter.Minute(), m.Enter.Second(), 0, time.UTC)

	for _, conflict := range conflicts {
		// I can tollerate if a Booking starts when the booking before ends. (b1 ends at 9:00, b2 starts at 9:00)
		t2Time := time.Date(0, 1, 1, conflict.Leave.Hour(), conflict.Leave.Minute(), conflict.Leave.Second(), 0, time.UTC)
		if !(t2Time.Before(t1Time) || t2Time.Equal(t1Time)) {
			return false, http.StatusBadRequest
		}
	}
	return true, 0
}

func (router *BookingRouter) copyFromRestModel(m *CreateBookingRequest, location *Location) (*Booking, error) {
	e := &Booking{}
	e.SpaceID = m.SpaceID
	e.Enter = m.Enter
	e.Leave = m.Leave
	enterNew, err := attachTimezoneInformation(e.Enter, location)
	if err != nil {
		return nil, err
	}
	e.Enter = enterNew
	leaveNew, err := attachTimezoneInformation(e.Leave, location)
	if err != nil {
		return nil, err
	}
	e.Leave = leaveNew
	return e, nil
}

func (router *BookingRouter) copyToRestModel(e *BookingDetails) *GetBookingResponse {
	m := &GetBookingResponse{}
	m.ID = e.ID
	m.UserID = e.UserID
	m.UserEmail = e.UserEmail
	m.SpaceID = e.SpaceID
	m.Enter, _ = attachTimezoneInformation(e.Enter, &e.Space.Location)
	m.Leave, _ = attachTimezoneInformation(e.Leave, &e.Space.Location)
	m.Space.ID = e.Space.ID
	m.Space.LocationID = e.Space.LocationID
	m.Space.Name = e.Space.Name
	m.Space.Location.ID = e.Space.Location.ID
	m.Space.Location.Name = e.Space.Location.Name
	return m
}
