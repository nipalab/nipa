import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { desktop, type CommentInput } from "@/api/desktop";
import type { Notify } from "@/hooks/useLocks";
import type { Strategy } from "@/lib/mergeRequests";
import { errorMessage } from "@/lib/utils";

export type ReviewState = "approved" | "changes_requested" | "commented";

// useMergeRequest loads one request and wraps every action on it; each
// action refreshes the request, its activity and the list.
export function useMergeRequest(root: string, number: number, notify: Notify) {
  const queryClient = useQueryClient();
  const detail = useQuery({ queryKey: ["mr", root, number], queryFn: () => desktop.mergeRequest(root, number) });
  const activity = useQuery({ queryKey: ["mr", root, number, "activity"], queryFn: () => desktop.mergeRequestActivity(root, number) });

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ["mr", root, number] });
    void queryClient.invalidateQueries({ queryKey: ["mrs", root] });
  };
  const action = <A>(fn: (arg: A) => Promise<unknown>, failure: string, success?: (arg: A) => string) =>
    useMutation({
      mutationFn: fn,
      onSuccess: (_: unknown, arg: A) => {
        refresh();
        if (success) notify(success(arg), "success");
      },
      onError: (err: unknown) => notify(`${failure}: ${errorMessage(err)}`),
    });

  const comment = action((input: Partial<CommentInput> & { body: string }) => desktop.comment(root, number, input), "Could not comment");
  const reply = action(({ threadId, body }: { threadId: string; body: string }) => desktop.reply(root, number, threadId, body), "Could not reply");
  const resolve = action(({ threadId, resolved }: { threadId: string; resolved: boolean }) => desktop.resolveThread(root, number, threadId, resolved), "Could not update the thread");
  const review = action(
    ({ state, body }: { state: ReviewState; body: string }) => desktop.submitReview(root, number, state, body),
    "Could not submit the review",
    ({ state }) => (state === "approved" ? "Approved" : state === "changes_requested" ? "Requested changes" : "Review sent"),
  );
  const merge = action(
    ({ strategy, deleteSource }: { strategy: Strategy; deleteSource: boolean }) => desktop.mergeMergeRequest(root, number, strategy, deleteSource),
    "Could not merge",
    () => `Merged !${number}`,
  );
  const close = action(() => desktop.closeMergeRequest(root, number), "Could not close", () => `Closed !${number}`);
  const reopen = action(() => desktop.reopenMergeRequest(root, number), "Could not reopen", () => `Reopened !${number}`);
  const draft = action(
    (value: boolean) => desktop.setDraft(root, number, value),
    "Could not change the draft state",
    (value) => (value ? "Marked as draft" : "Marked ready for review"),
  );
  const edit = action(({ title, description }: { title: string; description: string }) => desktop.editMergeRequest(root, number, title, description), "Could not save", () => "Saved");

  const busy = [comment, reply, resolve, review, merge, close, reopen, draft, edit].some((m) => m.isPending);
  return { detail, activity, comment, reply, resolve, review, merge, close, reopen, draft, edit, busy };
}
