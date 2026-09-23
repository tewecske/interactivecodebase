package zioapp.shared

/** Checks shared by the backend and any client. */
def validTitle(title: String): Boolean = title.trim.nonEmpty && title.length <= maxTitle

val maxTitle: Int = 200
