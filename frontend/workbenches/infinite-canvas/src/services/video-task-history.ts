type TaskLog = {
    status: string;
    task?: { id: string };
    error?: string;
    failureSource?: "upstream";
};

function legacyLocalTimeout(log: TaskLog) {
    return log.status === "failed" && !log.failureSource && /超时|timed?\s*out|timeout/i.test(log.error || "");
}

export function canContinueVideoTask(log: TaskLog) {
    return Boolean(log.task?.id) && (["pending", "stopped", "interrupted"].includes(log.status) || legacyLocalTimeout(log));
}

export function shouldResumeVideoTask(log: TaskLog) {
    return Boolean(log.task?.id) && (log.status === "pending" || legacyLocalTimeout(log));
}
