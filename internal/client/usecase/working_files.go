package usecase

import "context"

// WorkingFiles lists the clone's working files like status sees them
// (ignored paths dropped unless tracked or staged) and its sparse prefixes.
func (d *Diff) WorkingFiles(ctx context.Context, root string) (files, sparse []string, err error) {
	if err := d.localRepo.Init(root); err != nil {
		return nil, nil, err
	}
	d.opened.Store(true)
	unlock, err := lockShared(d.localRepo)
	if err != nil {
		return nil, nil, err
	}
	defer unlock()
	cfg, err := d.localRepo.LoadConfig()
	if err != nil {
		return nil, nil, err
	}
	snapshot, err := d.localRepo.Snapshot()
	if err != nil {
		return nil, nil, err
	}
	staged, err := d.localRepo.ListStaged()
	if err != nil {
		return nil, nil, err
	}
	protected := make([]string, 0, len(snapshot.Files)+len(staged))
	for _, f := range snapshot.Files {
		protected = append(protected, f.Path)
	}
	protected = append(protected, staged...)
	ignores, err := newIgnoreState(root, protected)
	if err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	files, err = walkWorkingFiles(root, ignores)
	if err != nil {
		return nil, nil, err
	}
	return files, cfg.Sparse, nil
}
