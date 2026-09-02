package platform

import "errors"

func asWrapped(err error, target **wrapped) bool { return errors.As(err, target) }
