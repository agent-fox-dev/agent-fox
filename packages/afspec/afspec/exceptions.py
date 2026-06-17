"""Exception hierarchy for the afspec library."""


class SpecError(Exception):
    """Base exception for all afspec errors."""


class LoadError(SpecError):
    """Raised when loading a spec from disk fails."""


class SaveError(SpecError):
    """Raised when saving a spec to disk fails."""


class LifecycleError(SpecError):
    """Raised when a lifecycle transition is illegal."""


class IntentError(SpecError):
    """Raised when intent hash computation or verification fails."""


class BootstrapError(SpecError):
    """Raised when bootstrap finalization fails."""
