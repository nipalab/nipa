import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { desktop, type CommentInput, type Person } from "@/api/desktop";
import type { Notify } from "@/hooks/useLocks";
import type { Strategy } from "@/lib/mergeRequests";
import { errorMessage } from "@/lib/utils";
import { translate } from "@/i18n";

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
      onError: (err: unknown) => notify(translate("useMergeRequest.text", { failure, value: errorMessage(err) })),
    });

  const comment = action((input: Partial<CommentInput> & { body: string }) => desktop.comment(root, number, input), translate("useMergeRequest.couldNotComment"));
  const reply = action(({ threadId, body }: { threadId: string; body: string }) => desktop.reply(root, number, threadId, body), translate("useMergeRequest.couldNotReply"));
  const resolve = action(({ threadId, resolved }: { threadId: string; resolved: boolean }) => desktop.resolveThread(root, number, threadId, resolved), translate("useMergeRequest.couldNotUpdateTheThread"));
  const review = action(
    ({ state, body }: { state: ReviewState; body: string }) => desktop.submitReview(root, number, state, body),
    translate("useMergeRequest.couldNotSubmitTheReview"),
    ({ state }) => (state === "approved" ? translate("useMergeRequest.approved") : state === "changes_requested" ? translate("useMergeRequest.requestedChanges") : translate("useMergeRequest.reviewSent")),
  );
  const merge = action(
    ({ strategy, deleteSource }: { strategy: Strategy; deleteSource: boolean }) => desktop.mergeMergeRequest(root, number, strategy, deleteSource),
    translate("useMergeRequest.couldNotMerge"),
    () => translate("useMergeRequest.merged", { number }),
  );
  const close = action(() => desktop.closeMergeRequest(root, number), translate("useMergeRequest.couldNotClose"), () => translate("useMergeRequest.closed", { number }));
  const reopen = action(() => desktop.reopenMergeRequest(root, number), translate("useMergeRequest.couldNotReopen"), () => translate("useMergeRequest.reopened", { number }));
  const draft = action(
    (value: boolean) => desktop.setDraft(root, number, value),
    translate("useMergeRequest.couldNotChangeTheDraft"),
    (value) => (value ? translate("useMergeRequest.markedAsDraft") : translate("useMergeRequest.markedReadyForReview")),
  );
  const edit = action(({ title, description }: { title: string; description: string }) => desktop.editMergeRequest(root, number, title, description), translate("useMergeRequest.couldNotSave"), () => translate("useMergeRequest.saved"));

  const assign = action((ids: string[]) => desktop.assignMergeRequest(root, number, ids), translate("useMergeRequest.couldNotAssign"));
  const askReview = action((person: Person) => desktop.requestReview(root, number, person.id), translate("useMergeRequest.couldNotRequestAReview"), (person) => translate("useMergeRequest.askedToReview", { name: person.name }));

  const busy = [comment, reply, resolve, review, merge, close, reopen, draft, edit, assign, askReview].some((m) => m.isPending);
  return { detail, activity, comment, reply, resolve, review, merge, close, reopen, draft, edit, assign, askReview, busy };
}
