package maintenance

import (
	"errors"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

const Domain = "nipa"

// GRPCError is the gRPC form of a rejection: Unavailable with an ErrorInfo
// detail and, while EndsAt is ahead, a RetryInfo.
func (e *Error) GRPCError(now time.Time) error {
	st := status.New(codes.Unavailable, e.Error())
	info := &errdetails.ErrorInfo{Reason: Reason, Domain: Domain, Metadata: e.Info.Metadata()}
	var detailed *status.Status
	var err error
	if d := e.Info.RetryAfter(now); d > 0 {
		detailed, err = st.WithDetails(info, &errdetails.RetryInfo{RetryDelay: durationpb.New(d)})
	} else {
		detailed, err = st.WithDetails(info)
	}
	if err != nil {
		return st.Err()
	}
	return detailed.Err()
}

// FromGRPC recognises a maintenance rejection in a gRPC error, wrapped or not.
func FromGRPC(err error) (*Error, bool) {
	if err == nil {
		return nil, false
	}
	var se interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &se) {
		return nil, false
	}
	st := se.GRPCStatus()
	if st.Code() != codes.Unavailable {
		return nil, false
	}
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.GetReason() == Reason && info.GetDomain() == Domain {
			return &Error{Info: InfoFromMetadata(info.GetMetadata())}, true
		}
	}
	return nil, false
}
